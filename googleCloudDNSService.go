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

	log.Debug().Msgf("Creating new GoogleCloudDNSService for project %v and zone %v", project, zone)

	ctx := context.Background()
	googleClient, err := google.DefaultClient(ctx, dns.NdevClouddnsReadwriteScope)
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

// GetDNSRecordByName returns the record sets matching name and type
func (dnsService *GoogleCloudDNSService) GetDNSRecordByName(dnsRecordType, dnsRecordName string) (records []*dns.ResourceRecordSet) {

	records = make([]*dns.ResourceRecordSet, 0)

	req := dnsService.service.ResourceRecordSets.List(dnsService.project, dnsService.zone).Name(dnsRecordName).Type(dnsRecordType)

	err := req.Pages(context.Background(), func(page *dns.ResourceRecordSetsListResponse) error {
		records = page.Rrsets
		return nil
	})

	if err != nil {
		log.Error().Err(err).Msgf("Failed retrieving records")
	}

	return
}

// UpsertDNSRecord either updates or creates a dns record.
func (dnsService *GoogleCloudDNSService) UpsertDNSRecord(dnsRecordType, dnsRecordName, dnsRecordContent string) (err error) {

	// retrieve records in case they exist
	records := dnsService.GetDNSRecordByName(dnsRecordType, dnsRecordName)

	change := dns.Change{
		Additions: []*dns.ResourceRecordSet{
			&dns.ResourceRecordSet{
				Name: fmt.Sprintf("%v.", dnsRecordName),
				Type: dnsRecordType,
				Ttl:  300,
				Rrdatas: []string{
					dnsRecordContent,
				},
				SignatureRrdatas: []string{},
				Kind:             "dns#resourceRecordSet",
			},
		},
	}

	if len(records) > 0 {
		// updating a record is done by deleting the current ones and adding the new one
		change.Deletions = records
	}

	resp, err := dnsService.service.Changes.Create(dnsService.project, dnsService.zone, &change).Context(context.Background()).Do()

	if err != nil {
		return err
	}

	log.Debug().Interface("response", resp).Msgf("Response from google cloud dns api")

	return
}
