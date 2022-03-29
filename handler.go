package main

import (
	"fmt"
	"net"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/spf13/afero"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
)

const (
	heraHostname = "hera.hostname"
	heraPort     = "hera.port"
	heraIP       = "hera.ip"
	heraProtocol = "hera.protocol"
)

// A Handler is responsible for responding to container start and die events
type Handler struct {
	Client *Client
}

// NewHandler returns a new Handler instance
func NewHandler(client *Client) *Handler {
	handler := &Handler{
		Client: client,
	}

	return handler
}

// HandleEvent dispatches an event to the appropriate handler method depending on its status
func (h *Handler) HandleEvent(event events.Message) {
	switch status := event.Status; status {
	case "start":
		err := h.handleStartEvent(event)
		if err != nil {
			log.Error(err.Error())
		}

	case "die":
		err := h.handleDieEvent(event)
		if err != nil {
			log.Error(err.Error())
		}
	}
}

// HandleContainer allows immediate tunnel creation when hera is started by treating existing
// containers as start events
func (h *Handler) HandleContainer(id string) error {
	event := events.Message{
		ID: id,
	}

	err := h.handleStartEvent(event)
	if err != nil {
		return err
	}

	return nil
}

// handleStartEvent inspects the container from a start event and creates a tunnel if the container
// has been appropriately labeled and a certificate exists for its hostname
func (h *Handler) handleStartEvent(event events.Message) error {
	container, err := h.Client.Inspect(event.ID)
	if err != nil {
		return err
	}

	hostname := getLabel(heraHostname, container)
	port := getLabel(heraPort, container)
	supplied_ip := getLabel(heraIP, container)
	protocol := getLabel(heraProtocol, container)
	if hostname == "" || port == "" {
		return nil
	}

	log.Infof("Container found, connecting to %s...", container.ID[:12])

	ip, err := h.resolveHostname(container)
	if err != nil {
		return err
	}

	// Check if an IP was supplied as label
	if supplied_ip != "" {
		ip = supplied_ip
	}

	// Check if a protocol was supplied as label
	if protocol == "" {
		protocol = "http"
	}

	cert, err := getCertificate(hostname)
	if err != nil {
		return err
	}

	config := &TunnelConfig{
		IP:       ip,
		Hostname: hostname,
		Port:     port,
		Protocol: protocol,
	}

	tunnel := NewTunnel(config, cert)
	tunnel.Start()

	return nil
}

// handleDieEvent inspects the container from a die event and stops the tunnel if one exists.
// An error is returned if a tunnel cannot be found or if the tunnel fails to stop
func (h *Handler) handleDieEvent(event events.Message) error {
	container, err := h.Client.Inspect(event.ID)
	if err != nil {
		return err
	}

	hostname := getLabel("hera.hostname", container)
	if hostname == "" {
		return nil
	}

	tunnel, err := GetTunnelForHost(hostname)
	if err != nil {
		return err
	}

	err = tunnel.Stop()
	if err != nil {
		return err
	}

	return nil
}

// resolveHostname returns the IP address of a container from its hostname.
// An error is returned if the hostname cannot be resolved after five attempts.
func (h *Handler) resolveHostname(container types.ContainerJSON) (string, error) {
	var resolved []string
	var err error

	attempts := 0
	maxAttempts := 5

	for attempts < maxAttempts {
		attempts++
		resolved, err = net.LookupHost(container.Config.Hostname)

		if err != nil {
			time.Sleep(2 * time.Second)
			log.Infof("Unable to connect, retrying... (%d/%d)", attempts, maxAttempts)

			continue
		}

		return resolved[0], nil
	}

	return "", fmt.Errorf("Unable to connect to %s", container.ID[:12])
}

// getLabel returns the label value from a given label name and container JSON.
func getLabel(name string, container types.ContainerJSON) string {
	value, ok := container.Config.Labels[name]
	if !ok {
		return ""
	}

	return value
}

// getCertificate returns a Certificate for a given hostname.
// An error is returned if the root hostname cannot be parsed or if the certificate cannot be found.
func getCertificate(hostname string) (*Certificate, error) {
	rootHostname, err := getRootDomain(hostname)
	if err != nil {
		return nil, err
	}

	cert, err := FindCertificateForHost(rootHostname, afero.NewOsFs())
	if err != nil {
		return nil, err
	}

	return cert, nil
}

// getRootDomain returns the root domain for a given hostname
func getRootDomain(hostname string) (string, error) {
	domain, err := publicsuffix.EffectiveTLDPlusOne(hostname)
	if err != nil {
		return "", err
	}

	return domain, nil
}
