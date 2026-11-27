// Command kagent-pig runs PiG as a kagent native Harness: it serves kagent's
// private A2A endpoint and drives one `pig --mode rpc` process per turn.
//
// kagent (Apache-2.0) defines the Harness runtime contract this program
// implements; Pi and PiG are separate projects. This program is not affiliated
// with or endorsed by either.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/kagent-dev/kagent/go/adk/pkg/app"
	"github.com/kagent-dev/kagent/go/adk/pkg/bootstrap"
	"github.com/kagent-dev/kagent/go/adk/pkg/controllerclient"
	runtimetaskstore "github.com/kagent-dev/kagent/go/adk/pkg/taskstore"
	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	runtimea2a "github.com/kagent-dev/kagent/go/harness/runtime/a2a"
	"github.com/kagent-dev/kagent/go/harness/runtime/continuation"
	"github.com/kagent-dev/kagent/go/pkg/logging"
	"github.com/kagent-dev/kagent/go/pkg/telemetry"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/config"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/driver"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/fsutil"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/session"
)

// Fixed layout of an Actor (design section 2). PiG's session lookup is
// cwd-scoped, so the workspace, session directory and session id are constants.
const (
	dataDir      = "/data"
	workspaceDir = "/data/workspace"
	sessionDir   = "/data/pig/sessions"
	pigHomeDir   = "/run/kagent/pig"
	privatePort  = "80"
)

func main() {
	check := flag.Bool("check", false, "validate configuration and the pig binary, then exit")
	flag.Parse()
	logger, err := logging.New(os.Stderr, "info")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx := logging.IntoContext(context.Background(), logger)
	deps := deps{
		getenv:    os.Getenv,
		lookup:    os.LookupEnv,
		environ:   os.Environ(),
		bootstrap: bootstrap.FromEnvironment,
		dataDir:   dataDir,
		pigHome:   pigHomeDir,
		tempDir:   os.TempDir(),
	}
	if err := run(ctx, *check, deps); err != nil {
		logger.ErrorContext(ctx, "pig harness stopped", "error", err)
		os.Exit(1)
	}
}

// deps are the process inputs, injectable for tests.
type deps struct {
	getenv    func(string) string
	lookup    func(string) (string, bool)
	environ   []string
	bootstrap func(context.Context, func(string) string) (*bootstrapBundle, error)
	dataDir   string
	pigHome   string
	tempDir   string
}

func run(ctx context.Context, check bool, d deps) error {
	bundle, err := d.bootstrap(ctx, d.getenv)
	if err != nil {
		return err
	}
	var card a2atype.AgentCard
	if err := json.Unmarshal(bundle.AgentCardJson, &card); err != nil {
		return fmt.Errorf("decode agent card: %w", err)
	}
	if strings.TrimSpace(card.Name) == "" {
		return errors.New("agent card name is required")
	}
	options, err := config.OptionsFromEnv(d.getenv)
	if err != nil {
		return err
	}
	compiled, err := config.Parse(bundle.ConfigJson, d.lookup, options)
	if err != nil {
		return err
	}
	logger, err := logging.New(os.Stderr, cmp.Or(compiled.LogLevel, "info"))
	if err != nil {
		return err
	}
	ctx = logging.IntoContext(ctx, logger)
	for _, warning := range compiled.Warnings {
		logger.WarnContext(ctx, "configuration accepted but not honoured", "detail", warning)
	}
	if compiled.ControlPlaneURL == "" {
		return errors.New("runtime.control_plane_url is required")
	}
	compiled.Telemetry = compiled.Telemetry.WithEnvironment(d.environ)
	if err := compiled.Telemetry.Validate(); err != nil {
		return fmt.Errorf("invalid runtime telemetry: %w", err)
	}

	executable, err := resolvePig(d.getenv(config.EnvBin), d.dataDir)
	if err != nil {
		return fmt.Errorf("find the pig binary (%s): %w", config.EnvBin, err)
	}
	pigHome, err := preparePigHome(ctx, pigHomeChoice{explicit: d.getenv(config.EnvPigHome), intended: d.pigHome, tempDir: d.tempDir}, check)
	if err != nil {
		return err
	}
	caBundle, err := driver.CombineCABundle(driver.SystemCABundles, d.getenv("SSL_CERT_FILE"), filepath.Join(pigHome, "ca-bundle.pem"))
	if err != nil {
		return err
	}
	caller, err := driver.NewMCPCaller(driver.HeaderValues(compiled), caBundle)
	if err != nil {
		return err
	}
	// The Actor's task state lives in kagent's TaskStore. The restore asks it whether a parked task is still open, because
	// RevertSession fails a task whose /data comes back from a snapshot that still shows it waiting. The app shares this channel.
	var controller *controllerclient.Client
	var tasks driver.TaskStates
	if !check {
		controller, err = controllerclient.New(controllerclient.Config{APIURL: compiled.ControlPlaneURL, AgentName: card.Name})
		if err != nil {
			return fmt.Errorf("connect to the kagent controller: %w", err)
		}
		defer controller.Close()
		tasks = taskStates{store: runtimetaskstore.New(controller, apia2a.RuntimeIdentityPath)}
	}
	runner, err := driver.New(driver.Config{CABundle: caBundle, Tasks: tasks, Logger: logger,
		Executable: executable, Workspace: filepath.Join(d.dataDir, "workspace"), SessionDir: filepath.Join(d.dataDir, "pig", "sessions"),
		PigHome: pigHome, AdapterDir: filepath.Join(d.dataDir, "adapter"), Compiled: compiled, Environment: d.environ,
		PassEnv: options.PassEnv, Tools: caller,
	})
	if err != nil {
		return err
	}
	validateCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	version, err := runner.Validate(validateCtx)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "pig harness configured", "pig", executable, "version", version, "provider", compiled.Provider, "model", compiled.Model,
		"mcp_servers", len(compiled.Policy.MCPServers), "tools", compiled.ToolsArg())
	if check {
		return nil
	}

	providers, err := telemetry.Init(ctx, telemetry.Options{
		Config: compiled.Telemetry.ExporterConfig(), Identity: compiled.Telemetry.ResourceIdentity(), Defaults: compiled.Telemetry.ResourceDefaults(card.Name),
		// Substrate restores every session from one snapshot taken after Init, so metrics
		// started here would give all sessions one identity.
		DeferMetrics: true,
	})
	if err != nil {
		logger.ErrorContext(ctx, "failed to initialize harness telemetry", "error", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := providers.Shutdown(shutdownCtx); err != nil {
			logger.ErrorContext(ctx, "failed to shutdown harness telemetry", "error", err)
		}
	}()

	store, err := continuation.New(filepath.Join(d.dataDir, "adapter"), "pig", session.ValidID)
	if err != nil {
		return err
	}
	executor, err := runtimea2a.New(runner, store, compiled.Telemetry)
	if err != nil {
		return err
	}
	application, err := app.New(app.AppConfig{
		ControllerClient: controller, AgentCard: card, Port: privatePort, AppName: card.Name, ControlPlaneURL: compiled.ControlPlaneURL, MaxContentLength: compiled.MaxContentLength,
		Logger: logger, Telemetry: compiled.Telemetry, Flush: providers.ForceFlush,
		HandlerOpts: []a2asrv.RequestHandlerOption{a2asrv.WithExecutorContextInterceptor(startMetrics(providers.StartMetrics))},
	}, executor)
	if err != nil {
		return fmt.Errorf("construct private A2A app: %w", err)
	}
	return application.Run()
}

// defaultPig is where the image installs pig. It is never looked up on PATH.
const defaultPig = "/usr/local/bin/pig"

// resolvePig returns the pig binary to run: override (KAGENT_PIG_BIN) or defaultPig. There is no PATH lookup,
// because the durable data directory is writable by the model's tools and a planted `pig` there would sit between
// the adapter and the real one, able to rewrite the policy file and forge the event stream. The path must be
// absolute and, after symlinks are resolved, name an executable regular file owned by root or by this process's user
// that does not live under dataDir (neither as given nor as resolved).
func resolvePig(override, dataDir string) (string, error) {
	path := cmp.Or(override, defaultPig)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q is not an absolute path", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	dirs := []string{filepath.Clean(dataDir)}
	if real, err := filepath.EvalSymlinks(dataDir); err == nil {
		dirs = append(dirs, real)
	}
	for _, p := range []string{filepath.Clean(path), resolved} {
		for _, dir := range dirs {
			if underDir(p, dir) {
				return "", fmt.Errorf("%q is under the durable data directory %s, which the model can write to", p, dir)
			}
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if err := checkPigFile(info, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// underDir reports whether path is dir or inside it.
func underDir(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// pigHomeChoice names where the ephemeral PIG_HOME may live, in order of preference.
type pigHomeChoice struct {
	// explicit is KAGENT_PIG_HOME; when set it is used or startup fails.
	explicit string
	// intended is /run/kagent/pig, used when writable.
	intended string
	// tempDir is the OS temporary directory, the normal choice under Substrate where /run/kagent is a read-only
	// projection and /tmp is the writable tmpfs.
	tempDir string
}

// preparePigHome creates the ephemeral PIG_HOME (mode 0700). Order: KAGENT_PIG_HOME, /run/kagent/pig when
// writable, then <tmp>/kagent-pig-<uid>, logged once at INFO with the reason. Only when no choice is usable is
// anything logged at WARN, and startup fails. The directory is regenerated every turn, so nothing in it needs
// to survive. In check mode a throwaway directory is used and /run is left untouched.
func preparePigHome(ctx context.Context, choice pigHomeChoice, check bool) (string, error) {
	log := logging.FromContext(ctx)
	if check {
		temp, err := os.MkdirTemp("", "kagent-pig-check-")
		if err != nil {
			return "", fmt.Errorf("create check directory: %w", err)
		}
		return temp, nil
	}
	if choice.explicit != "" {
		if err := usableDir(choice.explicit); err != nil {
			return "", fmt.Errorf("%s %q is not usable: %w", config.EnvPigHome, choice.explicit, err)
		}
		return choice.explicit, nil
	}
	intendedErr := usableDir(choice.intended)
	if intendedErr == nil {
		return choice.intended, nil
	}
	fallback := filepath.Join(choice.tempDir, fmt.Sprintf("kagent-pig-%d", os.Getuid()))
	if err := usableDir(fallback); err != nil {
		log.WarnContext(ctx, "no usable PIG_HOME directory", "intended", choice.intended, "intended_error", intendedErr.Error(), "fallback", fallback, "fallback_error", err.Error())
		return "", fmt.Errorf("no usable PIG_HOME: %s: %v; %s: %w", choice.intended, intendedErr, fallback, err)
	}
	log.InfoContext(ctx, "using a temporary directory for PIG_HOME", "path", fallback, "reason", intendedErr.Error(), "intended", choice.intended)
	return fallback, nil
}

// usableDir creates dir with mode 0700 (rejecting symlinks and other owners' directories) and proves it is writable.
func usableDir(dir string) error {
	if dir == "" {
		return errors.New("no path")
	}
	if err := fsutil.EnsurePrivateDir(dir); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

// startMetrics starts metrics on the first execution, which runs after the snapshot restore.
type startMetrics func()

func (start startMetrics) Intercept(ctx context.Context, _ *a2asrv.ExecutorContext) (context.Context, error) {
	start()
	return ctx, nil
}

// taskStates answers the restore's question from kagent's TaskStore.
type taskStates struct{ store *runtimetaskstore.Store }

func (t taskStates) TaskState(ctx context.Context, taskID string) (a2atype.TaskState, error) {
	stored, err := t.store.Get(ctx, a2atype.TaskID(taskID))
	if err != nil {
		return "", err
	}
	return stored.Task.Status.State, nil
}
