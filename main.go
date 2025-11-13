package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/n3wscott/phonebook/internal/fswatch"
	"github.com/n3wscott/phonebook/internal/httpapi"
	"github.com/n3wscott/phonebook/internal/project"
)

const defaultDebounce = 250 * time.Millisecond

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return cmdServe(nil)
	}

	switch args[0] {
	case "serve":
		return cmdServe(args[1:])
	case "generate":
		return cmdGenerate(args[1:])
	case "validate":
		return cmdValidate(args[1:])
	default:
		// Backwards-compatible: treat as serve flags.
		return cmdServe(args)
	}
}

type serveFlags struct {
	dir      string
	addr     string
	basePath string
	outDir   string
	tlsCert  string
	tlsKey   string
	logLevel string
}

func cmdServe(args []string) error {
	flags, err := parseServeFlags(args)
	if err != nil {
		return err
	}
	logger, level := newLogger(flags.logLevel)

	builder := &project.Builder{Dir: flags.dir, Logger: logger}
	state, err := builder.Build()
	if err != nil {
		return fmt.Errorf("initial build failed: %w", err)
	}

	addr := flags.addr
	basePath := normalizeBasePath(flags.basePath)

	server := httpapi.NewServer(httpapi.Config{
		Addr:       addr,
		BasePath:   basePath,
		TLSCert:    flags.tlsCert,
		TLSKey:     flags.tlsKey,
		AllowDebug: level <= slog.LevelDebug,
	}, logger)
	server.Update(state.Contacts, state.Phonebook, state.LastUpdate)

	if flags.outDir != "" {
		if err := writeOutputs(flags.outDir, state); err != nil {
			return err
		}
	}

	logger.Info("serving phonebook", "addr", addr, "basePath", basePath, "contacts", len(state.Contacts))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	watcher, err := fswatch.New(flags.dir, defaultDebounce, logger)
	if err != nil {
		return err
	}
	if err := watcher.Start(ctx, func() {
		next, err := builder.Build()
		if err != nil {
			logger.Warn("rebuild failed", "err", err)
			return
		}
		server.Update(next.Contacts, next.Phonebook, next.LastUpdate)
		if flags.outDir != "" {
			if err := writeOutputs(flags.outDir, next); err != nil {
				logger.Warn("failed to write outputs", "err", err)
			}
		}
		logger.Info("reloaded phonebook", "contacts", len(next.Contacts))
	}); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	select {
	case <-ctx.Done():
		<-errCh
		return nil
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func cmdGenerate(args []string) error {
	if len(args) == 0 {
		return errors.New("generate requires a subcommand: xml or asterisk")
	}
	switch args[0] {
	case "xml":
		return cmdGenerateXML(args[1:])
	case "asterisk":
		return cmdGenerateAsterisk(args[1:])
	default:
		return fmt.Errorf("unknown generate target %q", args[0])
	}
}

func cmdGenerateXML(args []string) error {
	fs := flag.NewFlagSet("generate xml", flag.ExitOnError)
	dir := fs.String("dir", "", "data root directory")
	out := fs.String("out", "", "output file or directory (phonebook.xml)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("--dir is required")
	}
	if *out == "" {
		return errors.New("--out is required")
	}
	logger, _ := newLogger("info")
	state, err := (&project.Builder{Dir: *dir, Logger: logger}).Build()
	if err != nil {
		return err
	}
	dest, err := resolveOutputPath(*out, "phonebook.xml")
	if err != nil {
		return err
	}
	return atomicWrite(dest, state.Phonebook, 0o644)
}

func cmdGenerateAsterisk(args []string) error {
	fs := flag.NewFlagSet("generate asterisk", flag.ExitOnError)
	dir := fs.String("dir", "", "data root directory")
	dest := fs.String("dest", "", "output directory for pjsip.conf and extensions.conf")
	apply := fs.Bool("apply", false, "atomically write to dest and reload Asterisk")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("--dir is required")
	}
	if *dest == "" {
		return errors.New("--dest is required")
	}

	logger, _ := newLogger("info")
	state, err := (&project.Builder{Dir: *dir, Logger: logger}).Build()
	if err != nil {
		return err
	}
	if err := writeOutputs(*dest, state); err != nil {
		return err
	}
	if *apply {
		if err := reloadAsterisk(); err != nil {
			return err
		}
	}
	return nil
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	dir := fs.String("dir", "", "data root directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("--dir is required")
	}
	logger, _ := newLogger("info")
	state, err := (&project.Builder{Dir: *dir, Logger: logger}).Build()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "ok: %d contacts\n", len(state.Contacts))
	return nil
}

func parseServeFlags(args []string) (serveFlags, error) {
	var flags serveFlags
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&flags.dir, "dir", getenv("PHONEBOOK_DIR", ""), "root directory containing config.yaml")
	fs.StringVar(&flags.dir, "d", getenv("PHONEBOOK_DIR", ""), "root directory containing config.yaml")
	fs.StringVar(&flags.addr, "addr", getenv("PHONEBOOK_ADDR", ":8080"), "HTTP listen address")
	fs.StringVar(&flags.basePath, "base-path", getenv("PHONEBOOK_BASE_PATH", "/"), "base HTTP path prefix")
	fs.StringVar(&flags.outDir, "out", getenv("PHONEBOOK_OUT", ""), "optional directory to stage pjsip.conf/extensions.conf")
	fs.StringVar(&flags.tlsCert, "tls-cert", getenv("PHONEBOOK_TLS_CERT", ""), "TLS certificate path")
	fs.StringVar(&flags.tlsKey, "tls-key", getenv("PHONEBOOK_TLS_KEY", ""), "TLS private key path")
	fs.StringVar(&flags.logLevel, "log-level", getenv("PHONEBOOK_LOG_LEVEL", "info"), "log level (debug, info, error)")
	if err := fs.Parse(args); err != nil {
		return flags, err
	}
	if flags.dir == "" {
		return flags, errors.New("--dir is required")
	}
	if (flags.tlsCert == "") != (flags.tlsKey == "") {
		return flags, errors.New("both --tls-cert and --tls-key must be provided together")
	}
	return flags, nil
}

func newLogger(level string) (*slog.Logger, slog.Level) {
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "error":
		lvl = slog.LevelError
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	return logger, lvl
}

func normalizeBasePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func writeOutputs(dir string, state project.State) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	pjsipPath := filepath.Join(dir, "pjsip.conf")
	extensionsPath := filepath.Join(dir, "extensions.conf")
	if err := atomicWrite(pjsipPath, state.PJSIP, 0o644); err != nil {
		return err
	}
	if err := atomicWrite(extensionsPath, state.Extensions, 0o644); err != nil {
		return err
	}
	return nil
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func resolveOutputPath(out, fileName string) (string, error) {
	info, err := os.Stat(out)
	if err == nil {
		if info.IsDir() {
			return filepath.Join(out, fileName), nil
		}
		return out, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if strings.HasSuffix(out, string(os.PathSeparator)) || filepath.Ext(out) == "" {
		if err := os.MkdirAll(out, 0o755); err != nil {
			return "", err
		}
		return filepath.Join(out, fileName), nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	return out, nil
}

func reloadAsterisk() error {
	commands := []string{"pjsip reload", "dialplan reload"}
	for _, cmd := range commands {
		c := exec.Command("asterisk", "-rx", cmd)
		output, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("asterisk %q failed: %v: %s", cmd, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func getenv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}
