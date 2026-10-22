// Package versioncheck reports the component versions used by a delegation.
package versioncheck

import (
	"fmt"
	"io"
	"strconv"
)

type Snapshot struct {
	Concierge, HerdrClient, HerdrServer, Agent   string
	Stale, PrivateCompatible, EndpointCompatible string
}

type Component struct {
	Side    string `json:"side"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Warning struct {
	Side         string `json:"side"`
	Component    string `json:"component"`
	Detail       string `json:"detail"`
	Step         string `json:"step"`
	Incompatible bool   `json:"incompatible,omitempty"`
}

type Report struct {
	Components []Component `json:"components"`
	Warnings   []Warning   `json:"warnings,omitempty"`
}

func Compare(primary Snapshot, machine *Snapshot, agent, label string) Report {
	var report Report
	addSide := func(side string, snapshot Snapshot) {
		for _, component := range []struct{ name, version string }{
			{"concierge", snapshot.Concierge}, {"herdr-client", snapshot.HerdrClient},
			{"herdr-server", snapshot.HerdrServer}, {agent, snapshot.Agent},
			{"server_binary_stale", snapshot.Stale},
			{"private_protocol_compatible", snapshot.PrivateCompatible},
			{"endpoint_protocol_compatible", snapshot.EndpointCompatible},
		} {
			value := component.version
			if value == "" {
				value = "unconfirmed"
			}
			report.Components = append(report.Components, Component{side, component.name, value})
			if component.version == "" {
				report.Warnings = append(
					report.Warnings,
					Warning{
						Side:      side,
						Component: component.name,
						Detail:    "version or compatibility is unconfirmed",
						Step:      repairStep(component.name, side, agent, label),
					},
				)
			}
		}
		if snapshot.Stale == "yes" {
			report.Warnings = append(
				report.Warnings,
				Warning{
					Side:      side,
					Component: "server_binary_stale",
					Detail:    "the running Herdr server uses a stale binary",
					Step:      repairStep("herdr-server", side, agent, label),
				},
			)
		}
		if snapshot.HerdrClient != "" && snapshot.HerdrServer != "" &&
			snapshot.HerdrClient != snapshot.HerdrServer {
			report.Warnings = append(
				report.Warnings,
				Warning{
					Side:      side,
					Component: "herdr-server",
					Detail:    "client " + snapshot.HerdrClient + " differs from running server " + snapshot.HerdrServer,
					Step:      repairStep("herdr-server", side, agent, label),
				},
			)
		}
		for _, protocol := range []struct{ name, compatible string }{{"private_protocol", snapshot.PrivateCompatible}, {"endpoint_protocol", snapshot.EndpointCompatible}} {
			if protocol.compatible == "no" {
				report.Warnings = append(
					report.Warnings,
					Warning{
						Side:         side,
						Component:    protocol.name,
						Detail:       "Herdr reports an incompatible protocol; delegation is blocked",
						Step:         repairStep("herdr-server", side, agent, label),
						Incompatible: true,
					},
				)
			}
		}
	}
	addSide("primary", primary)
	if machine == nil {
		return report
	}
	addSide("machine", *machine)
	for _, component := range []struct{ name, primary, remote string }{
		{"concierge", primary.Concierge, machine.Concierge},
		{"herdr-client", primary.HerdrClient, machine.HerdrClient},
		{"herdr-server", primary.HerdrServer, machine.HerdrServer},
		{agent, primary.Agent, machine.Agent},
	} {
		if component.primary != "" && component.remote != "" &&
			component.primary != component.remote {
			report.Warnings = append(
				report.Warnings,
				Warning{
					Side:      "machine",
					Component: component.name,
					Detail:    "primary " + component.primary + " differs from machine " + component.remote,
					Step:      repairStep(component.name, "machine", agent, label),
				},
			)
		}
	}
	return report
}

func repairStep(component, side, agent, label string) string {
	location := "On the primary, "
	if side == "machine" {
		location = "On machine " + strconv.Quote(label) + ", "
	}
	switch component {
	case "concierge":
		return "On the primary, restore the adopted Concierge shell, run concierge host restart, then run concierge machine prepare " + strconv.Quote(
			label,
		) + " --client-source /absolute/path/to/concierge at the host commit."
	case "herdr-client":
		return location + "install the same Herdr release as the primary; run herdr status to verify the client and server."
	case agent:
		return location + "install the same " + agent + " release as the primary; run " + agent + " --version to verify."
	default:
		return location + "save active work before a restart. Run herdr server stop, then herdr, then herdr status. Require matching versions, server_binary_stale: no, and compatible protocols."
	}
}

func (report Report) Blocked() bool {
	for _, warning := range report.Warnings {
		if warning.Incompatible {
			return true
		}
	}
	return false
}

func (report Report) Write(output io.Writer) error {
	if _, err := fmt.Fprintf(output, "versions[%d]{side,component,value}:\n", len(report.Components)); err != nil {
		return err
	}
	for _, component := range report.Components {
		if _, err := fmt.Fprintf(output, "  %s,%s,%s\n", strconv.Quote(component.Side), strconv.Quote(component.Name), strconv.Quote(component.Version)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(output, "version_warnings[%d]{side,component,detail,help}:\n", len(report.Warnings)); err != nil {
		return err
	}
	for _, warning := range report.Warnings {
		if _, err := fmt.Fprintf(output, "  %s,%s,%s,%s\n", strconv.Quote(warning.Side), strconv.Quote(warning.Component), strconv.Quote(warning.Detail), strconv.Quote(warning.Step)); err != nil {
			return err
		}
	}
	return nil
}
