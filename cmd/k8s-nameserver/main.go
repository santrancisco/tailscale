// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build !plan9

// k8s-nameserver is a simple nameserver implementation meant to be used with
// k8s-operator to allow to resolve magicDNS names of Tailscale nodes in a
// Kubernetes cluster.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	operatorutils "tailscale.com/k8s-operator"
	"tailscale.com/net/dns/resolver"
	"tailscale.com/net/tsdial"
	"tailscale.com/types/logger"
	"tailscale.com/util/dnsname"
)

const (
	// defaultDNSConfigDir is the location where, for the default nameserver
	// deployment, a Configmap with the hosts records will be mounted.
	defaultDNSConfigDir = "/config"
	defaultDNSFile      = "dns.json"
	udpEndpoint         = ":1053"

	kubeletMountedConfigLn = "..data"
)

var (
	tsnetRootDomains = []dnsname.FQDN{"ts.net", "ts.net."}
)

// nameserver is a simple nameserver that can respond to A record queries. It is
// intended to be used on Kubernetes to enable MagicDNS name resolution in
// cluster.
type nameserver struct {
	configReader  configReaderFunc
	configWatcher <-chan string
	res           *resolver.Resolver
	logger        logger.Logf
}

// configReaderFunc returns most up to date configuration for the nameserver.
type configReaderFunc func() ([]byte, error)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.Printf

	res := resolver.New(logger, nil, nil, &tsdial.Dialer{Logf: logger}, nil)

	// Read configuration for the nameserver from a file.
	var configReader configReaderFunc = func() ([]byte, error) {
		if contents, err := os.ReadFile(filepath.Join(defaultDNSConfigDir, defaultDNSFile)); err == nil {
			return contents, nil
		} else if os.IsNotExist(err) {
			return nil, nil
		} else {
			return nil, err
		}
	}

	c := make(chan string)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("error creating a new configfile watcher: %v", err)
	}
	defer watcher.Close()
	// kubelet mounts configmap to a Pod using a series of symlinks, one of
	// which is <mount-dir>/..data that Kubernetes recommends consumers to
	// use if they need to monitor changes
	// https://github.com/kubernetes/kubernetes/blob/v1.28.1/pkg/volume/util/atomic_writer.go#L39-L61
	toWatch := filepath.Join(defaultDNSConfigDir, kubeletMountedConfigLn)
	go func() {
		logger("starting file watch for %s", defaultDNSConfigDir)
		if err != nil {
			log.Fatalf("error starting a new configfile watcher: %v", err)
		}
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					logger("watcher finished")
					cancel()
					return
				}
				if event.Name == toWatch {
					msg := fmt.Sprintf("config update received: %s", event)
					logger(msg)
					c <- msg
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					logger("errors watcher finished: %v", err)
					cancel()
					return
				}
				if err != nil {
					logger("error watching directory: %w", err)
					cancel()
					return
				}
			}
		}
	}()
	if err = watcher.Add(defaultDNSConfigDir); err != nil {
		log.Fatalf("failed setting up file watch for DNS config: %v", err)
	}

	ns := &nameserver{
		configReader:  configReader,
		configWatcher: c,
		logger:        logger,
		res:           res,
	}

	if err := ns.run(ctx, cancel); err != nil {
		log.Fatalf("error running nameserver: %v", err)
	}

	addr, err := net.ResolveUDPAddr("udp", udpEndpoint)
	if err != nil {
		log.Fatalf("error resolving UDP address: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatalf("error opening udp connection: %v", err)
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	logger("ts.net nameserver listening on: %v", addr)

	for {
		logger("parsing a query")
		payloadBuff := make([]byte, 4096) // 4096 bytes is the recommended EDNS max payload size https://datatracker.ietf.org/doc/html/rfc6891#section-6.2.5
		_, _, _, addr, err := conn.ReadMsgUDP(payloadBuff, nil)
		if err != nil {
			logger(fmt.Sprintf("error reading UDP message: %v", err))
			return
		}
		go func() {
			dnsAnswer, err := ns.query(ctx, payloadBuff, addr.AddrPort())
			if err != nil {
				logger(fmt.Sprintf("error querying internal resolver: %v", err))
				// reply with the dnsAnswer anyway
			}
			n, err := conn.WriteToUDP(dnsAnswer, addr)
			if err != nil {
				logger("error writing UDP response: %v", err)
			} else {
				logger("written %d bytes in response", n)
			}
		}()
	}
}

// run ensures that resolver configuration is up to date with regards to its
// source. will update config once before returning and keep monitoring it in a
// thread.
func (n *nameserver) run(ctx context.Context, cancelF context.CancelFunc) error {
	n.logger("setting initial resolver config")
	if err := n.updateResolverConfig(); err != nil {
		return fmt.Errorf("error updating resolver config: %w", err)
	}
	n.logger("successfully updated resolver config")
	go func() {
		for {
			select {
			case <-ctx.Done():
				n.logger("nameserver exiting")
				return
			case <-n.configWatcher:
				n.logger("attempting to update resolver config...")
				if err := n.updateResolverConfig(); err != nil {
					n.logger("error updating resolver config: %w", err)
					cancelF()
				}
				n.logger("successfully updated resolver config")
			}
		}
	}()
	return nil
}

func (n *nameserver) query(ctx context.Context, payload []byte, add netip.AddrPort) ([]byte, error) {
	return n.res.Query(ctx, payload, "udp", add)
}

func (n *nameserver) updateResolverConfig() error {
	dnsCfgBytes, err := n.configReader()
	if err != nil {
		n.logger("error reading config: %v", err)
		return err
	}
	if dnsCfgBytes == nil || len(dnsCfgBytes) < 1 {
		n.logger("no DNS config provided")
		return nil
	}
	dnsCfg := &operatorutils.TSHosts{}
	err = json.Unmarshal(dnsCfgBytes, dnsCfg)
	if err != nil {
		n.logger("error unmarshaling json: %v", err)
		return err
	}
	if dnsCfg.Hosts == nil || len(dnsCfg.Hosts) == 0 {
		n.logger("no host records found")
	}
	c := resolver.Config{}

	// Ensure that queries for ts.net subdomains are never forwarded to
	// external resolvers.
	c.LocalDomains = tsnetRootDomains

	c.Hosts = make(map[dnsname.FQDN][]netip.Addr)
	for fqdn, ips := range dnsCfg.Hosts {
		fqdn, err := dnsname.ToFQDN(fqdn)
		if err != nil {
			n.logger("invalid DNS config: cannot convert %s to FQDN: %v", fqdn, err)
			return err
		}
		for _, ip := range ips {
			ip, err := netip.ParseAddr(ip)
			if err != nil {
				n.logger("invalid DNS config: cannot convert %s to netip.Addr: %v", ip, err)
				return err
			}
			c.Hosts[fqdn] = []netip.Addr{ip}
		}
	}
	// Resolver locks its config so this is safe for concurrent calls.
	n.res.SetConfig(c)
	return nil
}
