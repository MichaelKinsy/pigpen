package readashboard

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// provState is a provider's availability as `rea doctor --json` reports it.
type provState int

const (
	provUnknown       provState = iota
	provReady                   // configured and available
	provFailing                 // configured, but a check fails
	provNotConfigured           // nothing selects it
)

// provider is one analysis provider in the doctor report.
type provider struct {
	ID          string
	Label       string
	State       provState
	Version     string
	Detail      string
	Remediation string
	Checks      []providerCheck
}

type providerCheck struct {
	Name        string
	OK          bool
	Code        string
	Detail      string
	Remediation string
}

// doctorReport is what the dashboard reads of `rea doctor --json`.
type doctorReport struct {
	Healthy     bool
	Providers   []provider
	REAVersion  string // identity.cli_package_version
	Tools       int    // identity.catalog.counts.mcp_tools
	Checks      int
	FailedOther int // failing checks that are not provider checks (registrations of other clients, skills)
}

// The wire shape, from src/application/Doctor.ts (DoctorReport) and a captured `doctor --json`.
type doctorWire struct {
	Healthy         bool              `json:"healthy"`
	Checks          []doctorCheckWire `json:"checks"`
	ScopeChecks     []doctorCheckWire `json:"scope_checks"`
	HopperPath      string            `json:"hopperPath"`
	ProviderInspect []struct {
		ID         string  `json:"id"`
		Configured *bool   `json:"configured"`
		Available  *bool   `json:"available"`
		Version    *string `json:"providerVersion"`
		Checks     []struct {
			Name        string  `json:"name"`
			OK          bool    `json:"ok"`
			Code        *string `json:"code"`
			Detail      string  `json:"detail"`
			Remediation *string `json:"remediation"`
		} `json:"checks"`
	} `json:"providerInspections"`
	Identity struct {
		Version string `json:"cli_package_version"`
		Catalog struct {
			Counts struct {
				Tools int `json:"mcp_tools"`
			} `json:"counts"`
		} `json:"catalog"`
	} `json:"identity"`
}

type doctorCheckWire struct {
	Name           string `json:"name"`
	OK             bool   `json:"ok"`
	Classification string `json:"classification"`
	Detail         string `json:"detail"`
	Remediation    string `json:"remediation"`
}

// providerOrder is the order the header and the Providers tab list providers in; others follow by id.
var providerOrder = []string{"hopper", "ghidra", "ida", "jeb", "jadx", "kuna"}

var providerLabels = map[string]string{
	"hopper": "Hopper", "ghidra": "Ghidra", "ida": "IDA", "jeb": "JEB", "jadx": "JADX", "kuna": "Kuna", "rizin": "Rizin",
}

// checkProvider maps a top-level check name to the provider it checks.
var checkProvider = map[string]string{
	"hopper": "hopper", "ghidra": "ghidra", "ida-registration": "ida", "jadx": "jadx", "kuna-installation": "kuna",
}

func labelOf(id string) string {
	if l, ok := providerLabels[id]; ok {
		return l
	}
	if id == "" {
		return id
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

// parseDoctor decodes `rea doctor --json` output.
func parseDoctor(raw []byte) (*doctorReport, error) {
	var wire doctorWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	checks := wire.Checks
	if len(checks) == 0 {
		checks = wire.ScopeChecks
	}
	if len(checks) == 0 && len(wire.ProviderInspect) == 0 && wire.Identity.Version == "" {
		return nil, errors.New("not a doctor report")
	}
	report := &doctorReport{Healthy: wire.Healthy, REAVersion: wire.Identity.Version, Tools: wire.Identity.Catalog.Counts.Tools, Checks: len(checks)}
	byID := map[string]*provider{}

	// Provider-owned checks first: they carry configured and available.
	for _, in := range wire.ProviderInspect {
		p := &provider{ID: in.ID, Label: labelOf(in.ID)}
		if in.Version != nil {
			p.Version = *in.Version
		}
		for _, c := range in.Checks {
			pc := providerCheck{Name: c.Name, OK: c.OK, Detail: c.Detail}
			if c.Code != nil {
				pc.Code = *c.Code
			}
			if c.Remediation != nil {
				pc.Remediation = *c.Remediation
			}
			p.Checks = append(p.Checks, pc)
			if !c.OK && p.Detail == "" {
				p.Detail, p.Remediation = pc.Detail, pc.Remediation
			}
		}
		switch {
		case in.Configured != nil && !*in.Configured:
			p.State = provNotConfigured
		case in.Available != nil && *in.Available:
			p.State = provReady
		case in.Available != nil:
			p.State = provFailing
		default:
			p.State = provUnknown
		}
		byID[p.ID] = p
	}

	// Checks of providers the inspections did not cover (Hopper has none), and the non-provider failures.
	hopperOK, hopperSeen := true, false
	for _, c := range checks {
		id, isProvider := checkProvider[c.Name]
		if strings.HasPrefix(c.Name, "hopper") {
			id, isProvider = "hopper", true
		}
		if !isProvider {
			if !c.OK && c.Classification != "healthy" {
				report.FailedOther++
			}
			continue
		}
		if _, covered := byID[id]; covered && id != "hopper" {
			continue
		}
		if id == "hopper" {
			hopperSeen = true
			hopperOK = hopperOK && c.OK
		}
		p := byID[id]
		if p == nil {
			p = &provider{ID: id, Label: labelOf(id)}
			byID[id] = p
		}
		p.Checks = append(p.Checks, providerCheck{Name: c.Name, OK: c.OK, Detail: c.Detail, Remediation: c.Remediation})
		if !c.OK && p.Detail == "" {
			p.Detail, p.Remediation = c.Detail, c.Remediation
		}
	}
	if p := byID["hopper"]; p != nil && hopperSeen {
		switch {
		case hopperOK:
			p.State = provReady
			if wire.HopperPath != "" {
				p.Detail = wire.HopperPath
			}
		case p.Detail == "" && wire.HopperPath == "":
			// Doctor names no path and gives no detail: nothing selected Hopper.
			p.State = provNotConfigured
		default:
			p.State = provFailing
		}
	}
	// A provider known only from its top-level check.
	for id, p := range byID {
		if p.State != provUnknown || len(p.Checks) == 0 || id == "hopper" {
			continue
		}
		okAll := true
		for _, c := range p.Checks {
			okAll = okAll && c.OK
		}
		if okAll {
			p.State = provReady
		} else {
			p.State = provFailing
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	rank := func(id string) int {
		for i, o := range providerOrder {
			if o == id {
				return i
			}
		}
		return len(providerOrder)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ri, rj := rank(ids[i]), rank(ids[j]); ri != rj {
			return ri < rj
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		report.Providers = append(report.Providers, *byID[id])
	}
	return report, nil
}

// summary counts providers by state.
func (r *doctorReport) summary() (ready, failing, notConfigured int) {
	for _, p := range r.Providers {
		switch p.State {
		case provReady:
			ready++
		case provFailing:
			failing++
		case provNotConfigured:
			notConfigured++
		}
	}
	return
}

// envVar is an environment variable REA reads for a provider.
type envVar struct {
	Name string
	Note string
}

// providerEnv lists, per provider, the variables REA reads for it: the schema in REA's src/config/environment.ts
// for Hopper, Ghidra, IDA, JEB and the general settings, and src/android/JadxConfiguration.ts and
// src/kuna/KunaConfiguration.ts for JADX and Kuna. The dashboard only names them; it never sets them.
var providerEnv = map[string][]envVar{
	"hopper": {
		{"HOPPER_LAUNCHER_PATH", "Hopper launcher to use"},
		{"HOPPER_TARGET_PATH", "target Hopper opens"},
		{"HOPPER_TARGET_KIND", "executable or database"},
		{"HOPPER_LOADER_ARGS_JSON", "loader arguments, JSON"},
		{"REA_HOPPER_STARTUP_TIMEOUT_MS", "startup timeout"},
		{"REA_HOPPER_CALLER_SCAN_BUDGET_MS", "caller scan budget"},
	},
	"ghidra": {
		{"GHIDRA_INSTALL_DIR", "extracted Ghidra release directory"},
		{"JAVA_HOME", "JDK used to run Ghidra"},
		{"REA_GHIDRA_NATIVEAOT_JAR", "native AOT bridge JAR"},
		{"REA_GHIDRA_STARTUP_TIMEOUT_MS", "startup timeout"},
		{"REA_GHIDRA_DECOMPILE_TIMEOUT_SECONDS", "decompile timeout"},
		{"REA_GHIDRA_PROJECT_PATH", "existing project"},
		{"REA_GHIDRA_CHECKPOINT_DIR", "checkpoint directory"},
		{"REA_GHIDRA_PROGRAM_NAME", "program name in the project"},
		{"REA_GHIDRA_LANGUAGE_ID", "processor language"},
		{"REA_GHIDRA_COMPILER_SPEC_ID", "compiler spec"},
		{"REA_GHIDRA_DISABLED_ANALYZERS", "analyzers to skip"},
		{"REA_GHIDRA_SEED_FILE", "seed file"},
	},
	"ida": {
		{"REA_IDA_MCP_CONFIG", "absolute path of the IDA MCP registration JSON"},
	},
	"jeb": {
		{"REA_JEB_MCP_URL", "JEB MCP endpoint"},
		{"REA_JEB_MCP_TIMEOUT_MS", "startup timeout"},
	},
	"jadx": {
		{"REA_JADX_MCP_JAR", "absolute path of jadx-headless-mcp JAR"},
		{"REA_JADX_OPERATION_TIMEOUT_MS", "operation timeout"},
		{"REA_JADX_HEAP_MIB", "heap size"},
		{"REA_JADX_ACTIVE_PROCESSOR_COUNT", "processor count"},
	},
	"kuna": {
		{"REA_KUNA_COMMAND", "absolute path of the kuna executable"},
		{"REA_KUNA_CACHE_DIR", "cache directory"},
		{"REA_KUNA_CACHE_MAX_BYTES", "cache size limit"},
		{"REA_KUNA_JOBS", "parallel jobs"},
		{"REA_KUNA_TIMEOUT_MS", "operation timeout"},
		{"REA_KUNA_PROJECT_TIMEOUT_MS", "project timeout"},
	},
}

// generalEnv are settings that apply to every provider.
var generalEnv = []envVar{
	{"REA_ANALYSIS_PROVIDER", "provider selector, default auto"},
	{"REA_ILSPY_CMD_PATH", "ilspycmd for .NET decompilation"},
	{"REA_LOG_LEVEL", "log level"},
}
