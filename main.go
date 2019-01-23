package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	stdlog "log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/ericchiang/k8s"
	corev1 "github.com/ericchiang/k8s/apis/core/v1"
	v1beta1 "github.com/ericchiang/k8s/apis/extensions/v1beta1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const annotationGoogleCloudDNS string = "estafette.io/google-cloud-dns"
const annotationGoogleCloudDNSHostnames string = "estafette.io/google-cloud-dns-hostnames"

const annotationGoogleCloudDNSState string = "estafette.io/google-cloud-dns-state"

// GoogleCloudDNSState represents the state of the service at Google Cloud DNS
type GoogleCloudDNSState struct {
	Enabled   string `json:"enabled"`
	Hostnames string `json:"hostnames"`
	IPAddress string `json:"ipAddress"`
}

var (
	googleCloudDNSProject = kingpin.Flag("project", "The Google Cloud project id the Cloud DNS zone is configured in.").Envar("GOOGLE_CLOUD_DNS_PROJECT").Required().String()
	googleCloudDNSZone    = kingpin.Flag("zone", "The Google Cloud zone name to use Cloud DNS for.").Envar("GOOGLE_CLOUD_DNS_ZONE").Required().String()

	version   string
	branch    string
	revision  string
	buildDate string
	goVersion = runtime.Version()
)

var (
	addr = flag.String("listen-address", ":9101", "The address to listen on for HTTP requests.")

	// seed random number
	r = rand.New(rand.NewSource(time.Now().UnixNano()))

	// define prometheus counter
	dnsRecordsTotals = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "estafette_google_cloud_dns_record_totals",
			Help: "Number of updated Google Cloud DNS records.",
		},
		[]string{"namespace", "status", "initiator", "type"},
	)
)

func init() {
	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(dnsRecordsTotals)
}

func main() {

	// parse command line parameters
	flag.Parse()
	kingpin.Parse()

	// log as severity for stackdriver logging to recognize the level
	zerolog.LevelFieldName = "severity"

	// set some default fields added to all logs
	log.Logger = zerolog.New(os.Stdout).With().
		Timestamp().
		Str("app", "estafette-google-cloud-dns").
		Str("version", version).
		Logger()

	// use zerolog for any logs sent via standard log library
	stdlog.SetFlags(0)
	stdlog.SetOutput(log.Logger)

	// log startup message
	log.Info().
		Str("branch", branch).
		Str("revision", revision).
		Str("buildDate", buildDate).
		Str("goVersion", goVersion).
		Msg("Starting estafette-google-cloud-dns...")

	// create kubernetes api client
	kubeClient, err := k8s.NewInClusterClient()
	if err != nil {
		log.Fatal().Err(err).Msg("Creating Kubernetes api client failed")
	}

	// start prometheus
	go func() {
		log.Debug().
			Str("port", *addr).
			Msg("Serving Prometheus metrics...")

		http.Handle("/metrics", promhttp.Handler())

		if err := http.ListenAndServe(*addr, nil); err != nil {
			log.Fatal().Err(err).Msg("Starting Prometheus listener failed")
		}
	}()

	// create service to Google Cloud DNS
	dnsService := NewGoogleCloudDNSService(*googleCloudDNSProject, *googleCloudDNSZone)

	// define channel and wait group to gracefully shutdown the application
	gracefulShutdown := make(chan os.Signal)
	signal.Notify(gracefulShutdown, syscall.SIGTERM, syscall.SIGINT)
	waitGroup := &sync.WaitGroup{}

	// watch services for all namespaces
	go func(waitGroup *sync.WaitGroup) {
		// loop indefinitely
		for {
			log.Info().Msg("Watching services for all namespaces...")

			var service corev1.Service
			watcher, err := kubeClient.Watch(context.Background(), k8s.AllNamespaces, &service, k8s.Timeout(time.Duration(300)*time.Second))
			defer watcher.Close()

			if err != nil {
				log.Error().Err(err).Msg("WatchServices call failed")
			} else {
				// loop indefinitely, unless it errors
				for {
					service := new(corev1.Service)
					event, err := watcher.Next(service)
					if err != nil {
						log.Error().Err(err).Msg("Getting next event from service watcher failed")
						break
					}

					if event == k8s.EventAdded || event == k8s.EventModified {
						waitGroup.Add(1)
						status, err := processService(dnsService, kubeClient, service, fmt.Sprintf("watcher:%v", event))
						dnsRecordsTotals.With(prometheus.Labels{"namespace": *service.Metadata.Namespace, "status": status, "initiator": "watcher", "type": "service"}).Inc()
						waitGroup.Done()

						if err != nil {
							log.Error().Err(err).Msgf("Processing service %v.%v failed", *service.Metadata.Name, *service.Metadata.Namespace)
							continue
						}
					}
				}
			}

			// sleep random time between 22 and 37 seconds
			sleepTime := applyJitter(30)
			log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}
	}(waitGroup)

	// watch ingresses for all namespaces
	go func(waitGroup *sync.WaitGroup) {
		// loop indefinitely
		for {
			log.Info().Msg("Watching ingresses for all namespaces...")

			var ingress v1beta1.Ingress
			watcher, err := kubeClient.Watch(context.Background(), k8s.AllNamespaces, &ingress, k8s.Timeout(time.Duration(300)*time.Second))
			defer watcher.Close()

			if err != nil {
				log.Error().Err(err).Msg("WatchIngresses call failed")
			} else {
				// loop indefinitely, unless it errors
				for {
					ingress := new(v1beta1.Ingress)
					event, err := watcher.Next(ingress)
					if err != nil {
						log.Error().Err(err).Msg("Getting next event from ingress watcher failed")
						break
					}

					if event == k8s.EventAdded || event == k8s.EventModified {
						waitGroup.Add(1)
						status, err := processIngress(dnsService, kubeClient, ingress, fmt.Sprintf("watcher:%v", event))
						dnsRecordsTotals.With(prometheus.Labels{"namespace": *ingress.Metadata.Namespace, "status": status, "initiator": "watcher", "type": "ingress"}).Inc()
						waitGroup.Done()

						if err != nil {
							log.Error().Err(err).Msgf("Processing ingress %v.%v failed", *ingress.Metadata.Name, *ingress.Metadata.Namespace)
							continue
						}
					}
				}
			}

			// sleep random time between 22 and 37 seconds
			sleepTime := applyJitter(30)
			log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}
	}(waitGroup)

	go func(waitGroup *sync.WaitGroup) {
		// loop indefinitely
		for {

			// get services for all namespaces
			log.Info().Msg("Listing services for all namespaces...")
			var services corev1.ServiceList
			err := kubeClient.List(context.Background(), k8s.AllNamespaces, &services)
			if err != nil {
				log.Error().Err(err).Msg("ListServices call failed")
			}
			log.Info().Msgf("Cluster has %v services", len(services.Items))

			// loop all services
			for _, service := range services.Items {

				waitGroup.Add(1)
				status, err := processService(dnsService, kubeClient, service, "poller")
				dnsRecordsTotals.With(prometheus.Labels{"namespace": *service.Metadata.Namespace, "status": status, "initiator": "poller", "type": "service"}).Inc()
				waitGroup.Done()

				if err != nil {
					log.Error().Err(err).Msgf("Processing service %v.%v failed", *service.Metadata.Name, *service.Metadata.Namespace)
					continue
				}
			}

			// get ingresses for all namespaces
			log.Info().Msg("Listing ingresses for all namespaces...")
			var ingresses v1beta1.IngressList
			err = kubeClient.List(context.Background(), k8s.AllNamespaces, &ingresses)
			if err != nil {
				log.Error().Err(err).Msg("ListIngresses call failed")
			}
			log.Info().Msgf("Cluster has %v ingresses", len(ingresses.Items))

			// loop all ingresses
			for _, ingress := range ingresses.Items {

				waitGroup.Add(1)
				status, err := processIngress(dnsService, kubeClient, ingress, "poller")
				dnsRecordsTotals.With(prometheus.Labels{"namespace": *ingress.Metadata.Namespace, "status": status, "initiator": "poller", "type": "ingress"}).Inc()
				waitGroup.Done()

				if err != nil {
					log.Error().Err(err).Msgf("Processing ingress %v.%v failed", *ingress.Metadata.Name, *ingress.Metadata.Namespace)
					continue
				}
			}

			// sleep random time around 900 seconds
			sleepTime := applyJitter(900)
			log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}
	}(waitGroup)

	signalReceived := <-gracefulShutdown
	log.Info().
		Msgf("Received signal %v. Waiting on running tasks to finish...", signalReceived)

	waitGroup.Wait()

	log.Info().Msg("Shutting down...")
}

func applyJitter(input int) (output int) {

	deviation := int(0.25 * float64(input))

	return input - deviation + r.Intn(2*deviation)
}

func getDesiredServiceState(service *corev1.Service) (state GoogleCloudDNSState) {

	var ok bool

	state.Enabled, ok = service.Metadata.Annotations[annotationGoogleCloudDNS]
	if !ok {
		state.Enabled = "false"
	}
	state.Hostnames, ok = service.Metadata.Annotations[annotationGoogleCloudDNSHostnames]
	if !ok {
		state.Hostnames = ""
	}

	if *service.Spec.Type == "LoadBalancer" && len(service.Status.LoadBalancer.Ingress) > 0 {
		state.IPAddress = *service.Status.LoadBalancer.Ingress[0].Ip
	}

	return
}

func getCurrentServiceState(service *corev1.Service) (state GoogleCloudDNSState) {

	// get state stored in annotations if present or set to empty struct
	googleCloudDNSStateString, ok := service.Metadata.Annotations[annotationGoogleCloudDNSState]
	if !ok {
		// couldn't find saved state, setting to default struct
		state = GoogleCloudDNSState{}
		return
	}

	if err := json.Unmarshal([]byte(googleCloudDNSStateString), &state); err != nil {
		// couldn't deserialize, setting to default struct
		state = GoogleCloudDNSState{}
		return
	}

	// return deserialized state
	return
}

func makeServiceChanges(dnsService *GoogleCloudDNSService, client *k8s.Client, service *corev1.Service, initiator string, desiredState, currentState GoogleCloudDNSState) (status string, err error) {

	status = "failed"
	hasChanges := false

	// check if service has estafette.io/google-cloud-dns annotation and it's value is true and
	// check if service has estafette.io/google-cloud-dns-hostnames annotation and it's value is not empty and
	// check if type equals LoadBalancer and
	// check if LoadBalancer has an ip address
	if desiredState.Enabled == "true" && len(desiredState.Hostnames) > 0 && desiredState.IPAddress != "" {

		// update dns record if anything has changed compared to the stored state
		if desiredState.IPAddress != currentState.IPAddress ||
			desiredState.Hostnames != currentState.Hostnames {

			hasChanges = true

			// loop all hostnames
			hostnames := strings.Split(desiredState.Hostnames, ",")
			for _, hostname := range hostnames {

				// validate hostname, skip if invalid
				if !validateHostname(hostname) {
					log.Error().Err(err).Msgf("[%v] Service %v.%v - Invalid dns record %v, skipping", initiator, *service.Metadata.Name, *service.Metadata.Namespace, hostname)
					continue
				}

				log.Info().Msgf("[%v] Service %v.%v - Upserting dns record %v (A) to ip address %v...", initiator, *service.Metadata.Name, *service.Metadata.Namespace, hostname, desiredState.IPAddress)

				err := dnsService.UpsertDNSRecord("A", hostname, desiredState.IPAddress)
				if err != nil {
					log.Error().Err(err).Msgf("[%v] Service %v.%v - Upserting dns record %v (A) to ip address %v failed", initiator, *service.Metadata.Name, *service.Metadata.Namespace, hostname, desiredState.IPAddress)
					return status, err
				}
			}
		}
	}

	if hasChanges {

		// if any state property changed make sure to update all
		currentState = desiredState

		log.Info().Msgf("[%v] Service %v.%v - Updating service because state has changed...", initiator, *service.Metadata.Name, *service.Metadata.Namespace)

		// serialize state and store it in the annotation
		googleCloudDNSStateByteArray, err := json.Marshal(currentState)
		if err != nil {
			log.Error().Err(err).Msgf("[%v] Service %v.%v - Marshalling state failed", initiator, *service.Metadata.Name, *service.Metadata.Namespace)
			return status, err
		}
		service.Metadata.Annotations[annotationGoogleCloudDNSState] = string(googleCloudDNSStateByteArray)

		// update service, because the state annotations have changed
		err = client.Update(context.Background(), service)
		if err != nil {
			log.Error().Err(err).Msgf("[%v] Service %v.%v - Updating service state has failed", initiator, *service.Metadata.Name, *service.Metadata.Namespace)
			return status, err
		}

		status = "succeeded"

		log.Info().Msgf("[%v] Service %v.%v - Service has been updated successfully...", initiator, *service.Metadata.Name, *service.Metadata.Namespace)

		return status, nil
	}

	status = "skipped"

	return status, nil
}

func processService(dnsService *GoogleCloudDNSService, client *k8s.Client, service *corev1.Service, initiator string) (status string, err error) {

	status = "failed"

	if &service != nil && &service.Metadata != nil && &service.Metadata.Annotations != nil {

		desiredState := getDesiredServiceState(service)
		currentState := getCurrentServiceState(service)

		status, err = makeServiceChanges(dnsService, client, service, initiator, desiredState, currentState)

		return
	}

	status = "skipped"

	return status, nil
}

func getDesiredIngressState(ingress *v1beta1.Ingress) (state GoogleCloudDNSState) {

	var ok bool

	state.Enabled, ok = ingress.Metadata.Annotations[annotationGoogleCloudDNS]
	if !ok {
		state.Enabled = "false"
	}
	state.Hostnames, ok = ingress.Metadata.Annotations[annotationGoogleCloudDNSHostnames]
	if !ok {
		state.Hostnames = ""
	}

	if len(ingress.Status.LoadBalancer.Ingress) > 0 {
		state.IPAddress = *ingress.Status.LoadBalancer.Ingress[0].Ip
	}

	return
}

func getCurrentIngressState(ingress *v1beta1.Ingress) (state GoogleCloudDNSState) {

	// get state stored in annotations if present or set to empty struct
	googleCloudDNSStateString, ok := ingress.Metadata.Annotations[annotationGoogleCloudDNS]
	if !ok {
		// couldn't find saved state, setting to default struct
		state = GoogleCloudDNSState{}
		return
	}

	if err := json.Unmarshal([]byte(googleCloudDNSStateString), &state); err != nil {
		// couldn't deserialize, setting to default struct
		state = GoogleCloudDNSState{}
		return
	}

	// return deserialized state
	return
}

func makeIngressChanges(dnsService *GoogleCloudDNSService, client *k8s.Client, ingress *v1beta1.Ingress, initiator string, desiredState, currentState GoogleCloudDNSState) (status string, err error) {

	status = "failed"

	// check if ingress has estafette.io/google-cloud-dns annotation and it's value is true and
	// check if ingress has estafette.io/google-cloud-dns-hostnames annotation and it's value is not empty and
	// check if type equals LoadBalancer and
	// check if LoadBalancer has an ip address
	if desiredState.Enabled == "true" && len(desiredState.Hostnames) > 0 && desiredState.IPAddress != "" {

		// update dns record if anything has changed compared to the stored state
		if desiredState.IPAddress != currentState.IPAddress ||
			desiredState.Hostnames != currentState.Hostnames {

			// loop all hostnames
			hostnames := strings.Split(desiredState.Hostnames, ",")
			for _, hostname := range hostnames {

				// validate hostname, skip if invalid
				if !validateHostname(hostname) {
					log.Error().Err(err).Msgf("[%v] Service %v.%v - Invalid dns record %v, skipping", initiator, *ingress.Metadata.Name, *ingress.Metadata.Namespace, hostname)
					continue
				}

				log.Info().Msgf("[%v] Ingress %v.%v - Upserting dns record %v (A) to ip address %v...", initiator, *ingress.Metadata.Name, *ingress.Metadata.Namespace, hostname, desiredState.IPAddress)

				err := dnsService.UpsertDNSRecord("A", hostname, desiredState.IPAddress)
				if err != nil {
					log.Error().Err(err).Msgf("[%v] Ingress %v.%v - Upserting dns record %v (A) to ip address %v failed", initiator, *ingress.Metadata.Name, *ingress.Metadata.Namespace, hostname, desiredState.IPAddress)
					return status, err
				}
			}

			// if any state property changed make sure to update all
			currentState = desiredState

			log.Info().Msgf("[%v] Ingress %v.%v - Updating ingress because state has changed...", initiator, *ingress.Metadata.Name, *ingress.Metadata.Namespace)

			// serialize state and store it in the annotation
			googleCloudDNSStateByteArray, err := json.Marshal(currentState)
			if err != nil {
				log.Error().Err(err).Msgf("[%v] Ingress %v.%v - Marshalling state failed", initiator, *ingress.Metadata.Name, *ingress.Metadata.Namespace)
				return status, err
			}
			ingress.Metadata.Annotations[annotationGoogleCloudDNSState] = string(googleCloudDNSStateByteArray)

			// update ingress, because the state annotations have changed
			err = client.Update(context.Background(), ingress)
			if err != nil {
				log.Error().Err(err).Msgf("[%v] Ingress %v.%v - Updating ingress state has failed", initiator, *ingress.Metadata.Name, *ingress.Metadata.Namespace)
				return status, err
			}

			status = "succeeded"

			log.Info().Msgf("[%v] Ingress %v.%v - Ingress has been updated successfully...", initiator, *ingress.Metadata.Name, *ingress.Metadata.Namespace)

			return status, nil
		}
	}

	status = "skipped"

	return status, nil
}

func processIngress(dnsService *GoogleCloudDNSService, client *k8s.Client, ingress *v1beta1.Ingress, initiator string) (status string, err error) {

	status = "failed"

	if &ingress != nil && &ingress.Metadata != nil && &ingress.Metadata.Annotations != nil {

		desiredState := getDesiredIngressState(ingress)
		currentState := getCurrentIngressState(ingress)

		status, err = makeIngressChanges(dnsService, client, ingress, initiator, desiredState, currentState)

		return
	}

	status = "skipped"

	return status, nil
}

func validateHostname(hostname string) bool {
	dnsNameParts := strings.Split(hostname, ".")
	// we need at least a subdomain within a zone
	if len(dnsNameParts) < 2 {
		return false
	}
	// each label needs to be max 63 characters
	for _, label := range dnsNameParts {
		if len(label) > 63 {
			return false
		}
	}
	return true
}
