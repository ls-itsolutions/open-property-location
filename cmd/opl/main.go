// Command opl serves surroundings of a location from places.db and builds
// that database from OpenStreetMap data.
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	"github.com/ls-itsolutions/open-property-location/internal/demo"
	"github.com/ls-itsolutions/open-property-location/internal/geocode"
	"github.com/ls-itsolutions/open-property-location/internal/importer"
	"github.com/ls-itsolutions/open-property-location/internal/server"
	"github.com/ls-itsolutions/open-property-location/internal/transit"
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
  opl serve  [--data places.db] [--addr :8080] [--demo]
  opl import --in places.geojsonseq [--routes routes.osm.gz] --out places.db --region <name> --data-version YYYY-MM-DD
  opl filter-expressions [--routes]
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
		err = filterExpressions(args)
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
	withDemo := fs.Bool("demo", false, "also serve a demo page at /demo/")
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

	handler := server.New(places, cats, geocoder)
	if *withDemo {
		mux := http.NewServeMux()
		mux.Handle("/", handler)
		mux.Handle("GET /{$}", http.RedirectHandler("demo/", http.StatusFound))
		mux.Handle("GET /demo/", demo.Handler())
		handler = mux
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", *addr, "data", *data, "dataVersion", places.DataVersion())
	if *withDemo {
		slog.Info("demo ready", "url", demoURL(*addr))
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// demoURL guesses the browser URL of the demo page. In a container the
// published port can differ from the one the server listens on.
func demoURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "/demo/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port) + "/demo/"
}

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	in := fs.String("in", "", "GeoJSON sequence from osmium export")
	routes := fs.String("routes", "", "optional OSM XML (.osm or .osm.gz) with route and stop_area relations, for the lines of stops")
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
	start := time.Now()
	var lines *transit.Lines
	if *routes != "" {
		if lines, err = readLines(*routes); err != nil {
			return err
		}
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()

	counts, err := importer.Import(cats, f, lines, *out, importer.Meta{Region: *region, DataVersion: *dataVersion})
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

// readLines reads route relations from OSM XML, gzipped if the name ends in
// .gz.
func readLines(path string) (*transit.Lines, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = bufio.NewReader(f)
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		defer gz.Close()
		r = gz
	}
	lines, err := transit.ReadLines(r)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return lines, nil
}

func filterExpressions(args []string) error {
	fs := flag.NewFlagSet("filter-expressions", flag.ExitOnError)
	routes := fs.Bool("routes", false, "print the expressions for route and stop_area relations instead")
	fs.Parse(args)

	exprs := transit.FilterExpressions()
	if !*routes {
		cats, err := categories.Load()
		if err != nil {
			return err
		}
		exprs = cats.FilterExpressions()
	}
	fmt.Println(strings.Join(exprs, "\n"))
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
