package main

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/dns/v1"
)

// GoogleCloudDNSService is the service that allows to create or update dns records
type GoogleCloudDNSService struct {
	service *dns.Service
	project string
	zone    string
}

// NewGoogleCloudDNSService returns an initialized APIClient
func NewGoogleCloudDNSService(project, zone string) *GoogleCloudDNSService {

	ctx := context.Background()
	googleClient, err := google.DefaultClient(ctx, dns.CloudPlatformScope, dns.NdevClouddnsReadwriteScope)
	if err != nil {
		log.Fatal().Err(err).Msg("Creating google cloud client failed")
	}

	dnsService, err := dns.New(googleClient)
	if err != nil {
		log.Fatal().Err(err).Msg("Creating google cloud dns service failed")
	}

	return &GoogleCloudDNSService{
		service: dnsService,
		project: project,
		zone:    zone,
	}
}

// UpsertDNSRecord either updates or creates a dns record.
func (dnsService *GoogleCloudDNSService) UpsertDNSRecord(dnsRecordType, dnsRecordName, dnsRecordContent string) (err error) {

	record := dns.ResourceRecordSet{
		Name: fmt.Sprintf("%v.", dnsRecordName),
		Rrdatas: []string{
			dnsRecordContent,
		},
		Type: dnsRecordType,
		Ttl:  300,
	}

	log.Debug().Interface("record", record).Msgf("Record sent to google cloud dns api")

	resp, err := dnsService.service.Changes.Create(dnsService.project, dnsService.zone, &dns.Change{
		Additions: []*dns.ResourceRecordSet{
			&record,
		},
	}).Context(context.Background()).Do()

	log.Debug().Interface("response", resp).Msgf("Response from google cloud dns api")

	if err != nil {
		return err
	}

	return
}
