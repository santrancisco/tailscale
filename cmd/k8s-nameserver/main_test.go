// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build !plan9

package main

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/dns/dnsmessage"
	"tailscale.com/net/dns/resolver"
	"tailscale.com/net/tsdial"
	"tailscale.com/tstest"
)

func TestNameserver(t *testing.T) {

	// Setup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hostConfig := `{"hosts":{"foo.bar.ts.net.": ["10.20.30.40"]}}`

	var mockConfigReader configReaderFunc = func() ([]byte, error) {
		return []byte(hostConfig), nil
	}
	configWatcher := make(chan string)
	logger := log.Printf
	res := resolver.New(logger, nil, nil, &tsdial.Dialer{Logf: logger}, nil)

	ns := &nameserver{
		configReader:  mockConfigReader,
		configWatcher: configWatcher,
		logger:        logger,
		res:           res,
	}
	if err := ns.run(ctx, cancel); err != nil {
		t.Fatalf("running nameserver: %v", err)
	}

	// Test that nameserver can resolve a DNS name from provided hosts config

	wantedResponse := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:                 0x0,
			Response:           true,
			OpCode:             0,
			Authoritative:      true,
			Truncated:          false,
			RecursionDesired:   false,
			RecursionAvailable: false,
			AuthenticData:      false,
			CheckingDisabled:   false,
			RCode:              dnsmessage.RCodeSuccess,
		},

		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{
				Name:   dnsmessage.MustNewName("foo.bar.ts.net."),
				Type:   dnsmessage.TypeA,
				Class:  dnsmessage.ClassINET,
				TTL:    0x258,
				Length: 0x4,
			},
			Body: &dnsmessage.AResource{
				A: [4]byte{10, 20, 30, 40},
			},
		}},
		Questions: []dnsmessage.Question{
			{
				Name:  dnsmessage.MustNewName("foo.bar.ts.net."),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
			},
		},
		Additionals: []dnsmessage.Resource{},
		Authorities: []dnsmessage.Resource{},
	}
	testQuery := dnsmessage.Message{
		Header: dnsmessage.Header{Authoritative: true},
		Questions: []dnsmessage.Question{
			{
				Name:  dnsmessage.MustNewName("foo.bar.ts.net."),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
			},
		},
	}
	testAddr, err := netip.ParseAddrPort("10.40.30.20:0")
	if err != nil {
		t.Fatalf("parsing address: %v", err)
	}
	packedTestQuery, err := testQuery.Pack()
	if err != nil {
		t.Fatalf("packing test query: %v", err)
	}
	answer, err := ns.query(ctx, packedTestQuery, testAddr)
	if err != nil {
		t.Fatalf("querying nameserver: %v", err)
	}
	var gotResponse dnsmessage.Message
	if err := gotResponse.Unpack(answer); err != nil {
		t.Fatalf("unpacking DNS response: %v", err)
	}
	if diff := cmp.Diff(gotResponse, wantedResponse); diff != "" {
		t.Fatalf("unexpected response (-got +want): \n%s", diff)
	}

	// Test that nameserver's hosts config gets dynamically updated

	newHostConfig := `{"hosts": {"baz.bar.ts.net.": ["10.40.30.20"]}}`
	var newMockConfigReader configReaderFunc = func() ([]byte, error) {
		return []byte(newHostConfig), nil
	}
	ns.configReader = newMockConfigReader

	timeout := 3 * time.Second
	timer := time.NewTimer(timeout)
	select {
	case <-timer.C:
		t.Fatalf("nameserver failed to process config update within %v", timeout)
	case configWatcher <- "config update":
	}
	wantedResponse = dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:                 0x0,
			Response:           true,
			OpCode:             0,
			Authoritative:      true,
			Truncated:          false,
			RecursionDesired:   false,
			RecursionAvailable: false,
			AuthenticData:      false,
			CheckingDisabled:   false,
			RCode:              dnsmessage.RCodeSuccess,
		},

		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{
				Name:   dnsmessage.MustNewName("baz.bar.ts.net."),
				Type:   dnsmessage.TypeA,
				Class:  dnsmessage.ClassINET,
				TTL:    0x258,
				Length: 0x4,
			},
			Body: &dnsmessage.AResource{
				A: [4]byte{10, 40, 30, 20},
			},
		}},
		Questions: []dnsmessage.Question{
			{
				Name:  dnsmessage.MustNewName("baz.bar.ts.net."),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
			},
		},
		Additionals: []dnsmessage.Resource{},
		Authorities: []dnsmessage.Resource{},
	}
	testQuery = dnsmessage.Message{
		Header: dnsmessage.Header{Authoritative: true},
		Questions: []dnsmessage.Question{
			{
				Name:  dnsmessage.MustNewName("baz.bar.ts.net."),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
			},
		},
	}
	packedTestQuery, err = testQuery.Pack()
	if err != nil {
		t.Fatalf("packing test query after update: %v", err)
	}

	// Retry a couple times as the nameserver will have eventually processed
	// the update.
	checker := func() error {
		answer, err := ns.query(ctx, packedTestQuery, testAddr)
		if err != nil {
			t.Fatalf("querying nameserver after update: %v", err)
		}
		gotResponse = dnsmessage.Message{}
		if err := gotResponse.Unpack(answer); err != nil {
			t.Fatalf("error unpacking DNS answer: %v", err)
		}
		if diff := cmp.Diff(gotResponse, wantedResponse); diff != "" {
			return fmt.Errorf("did not get expected response: (-got, +want): \n%s", diff)
		}
		return nil
	}
	if err := tstest.WaitFor(time.Second*10, checker); err != nil {
		t.Fatalf("failed waiting for nameserver's config to be updated: %v", err)
	}
}
