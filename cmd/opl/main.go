// Command opl serves surroundings of a location from places.db and builds
// that database from OpenStreetMap data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ls-itsolutions/open-property-location/internal/categories"
	"github.com/ls-itsolutions/open-property-location/internal/geocode"
	"github.com/ls-itsolutions/open-property-location/internal/importer"
	"github.com/ls-itsolutions/open-property-location/internal/server"
)

const (
	defaultData   = "places.db"
	defaultAddr   = ":8080"
	defaultPhoton = "https://photon.komoot.io"

	readHeaderTimeout  = 5 * time.Second
	writeTimeout       = 30 * time.Second
	idleTimeout        = 60 * time.Second
	shutdownTimeout    = 10 * time.Second
	healthcheckTimeout = 3 * time.Second
)

const usage = `Usage:
  opl serve  [--data places.db] [--addr :8080]
  opl import --in places.geojsonseq --out places.db --region <name> --data-version YYYY-MM-DD
  opl filter-expressions
  opl healthcheck [--addr :8080]

Environment: OPL_DATA, OPL_ADDR, PHOTON_URL (default https://photon.komoot.io)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "import":
		err = runImport(args)
	case "filter-expressions":
		err = filterExpressions()
	case "healthcheck":
		err = healthcheck(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "opl:", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	data := fs.String("data", env("OPL_DATA", defaultData), "path to places.db")
	addr := fs.String("addr", env("OPL_ADDR", defaultAddr), "listen address")
	fs.Parse(args)

	cats, err := categories.Load()
	if err != nil {
		return err
	}
	geocoder, err := geocode.New(env("PHOTON_URL", defaultPhoton))
	if err != nil {
		return err
	}
	places, err := server.Open(*data)
	if err != nil {
		return err
	}
	defer places.Close()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(places, cats, geocoder),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", *addr, "data", *data, "dataVersion", places.DataVersion())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	in := fs.String("in", "", "GeoJSON sequence from osmium export")
	out := fs.String("out", "", "database to create")
	region := fs.String("region", "", "region name stored in the database, e.g. germany")
	dataVersion := fs.String("data-version", "", "date of the OSM extract, YYYY-MM-DD")
	fs.Parse(args)
	if *in == "" || *out == "" {
		return errors.New("import: --in and --out are required")
	}

	cats, err := categories.Load()
	if err != nil {
		return err
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()

	start := time.Now()
	counts, err := importer.Import(cats, f, *out, importer.Meta{Region: *region, DataVersion: *dataVersion})
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(counts))
	total := 0
	for id, n := range counts {
		ids = append(ids, id)
		total += n
	}
	slices.Sort(ids)
	for _, id := range ids {
		fmt.Printf("%-16s %d\n", id, counts[id])
	}
	fmt.Printf("imported %d places into %s in %s\n", total, *out, time.Since(start).Round(time.Second))
	return nil
}

func filterExpressions() error {
	cats, err := categories.Load()
	if err != nil {
		return err
	}
	fmt.Println(strings.Join(cats.FilterExpressions(), "\n"))
	return nil
}

// healthcheck queries /health of a server running on this host. The
// distroless image has no shell or curl, so the Docker healthcheck uses it.
func healthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	addr := fs.String("addr", env("OPL_ADDR", defaultAddr), "listen address of the server")
	fs.Parse(args)

	_, port, err := net.SplitHostPort(*addr)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: healthcheckTimeout}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/health")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned status %d", resp.StatusCode)
	}
	return nil
}
