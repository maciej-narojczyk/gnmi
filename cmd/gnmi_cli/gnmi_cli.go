/*
Copyright 2017 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// The gnmi_cli program implements the GNMI CLI.
//
// usage:
//
//	gnmi_cli --address=<ADDRESS>                            \
//	           -q=<OPENCONFIG_PATH[,OPENCONFIG_PATH[,...]]> \
//	           [-qt=<QUERY_TYPE>]                           \
//	          [-<ADDITIONAL_OPTION(s)>]
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"context"
	"flag"

	log "github.com/golang/glog"
	"golang.org/x/crypto/ssh/terminal"
	"google.golang.org/protobuf/encoding/prototext"
	"github.com/golang/protobuf/proto"
	"github.com/jipanyang/gnmi/cli"
	"github.com/openconfig/gnmi/client"
	"github.com/openconfig/gnmi/client/flags"
	"golang.org/x/crypto/ssh/terminal"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	// Register supported client types.
	gclient "github.com/jipanyang/gnmi/client/gnmi"
)

var (
	displayHandle = os.Stdout
	prefix = []byte("[\n")
	rcvd_cnt uint = 0
	term = make(chan string, 1)	
	q   = client.Query{TLS: &tls.Config{}}
	mu  sync.Mutex
	cfg = cli.Config{Display: func(b []byte) {
		found := len(*expected_event) == 0
		if !found {
			var fvp map[string]interface{}

			json.Unmarshal(b, &fvp)
			_, found = fvp[*expected_event]
		}
		if found {
			defer mu.Unlock()
			mu.Lock()

			if *expected_cnt > 0 {
				rcvd_cnt += 1
			}
			displayHandle.Write(prefix)
			displayHandle.Write(b)
			prefix = []byte(",\n")
		}
	}}

	clientTypes = flags.NewStringList(&cfg.ClientTypes, []string{gclient.Type})
	queryFlag   = &flags.StringList{}
	queryType   = flag.String("query_type", client.Once.String(), "Type of result, one of: (o, once, p, polling, s, streaming).")
	queryAddr   = flags.NewStringList(&q.Addrs, nil)

	reqProto  = flag.String("proto", "", "Text proto for gNMI request.")
	protoFile = flag.String("proto_file", "", "Text proto file for gNMI request.")

	capabilitiesFlag = flag.Bool("capabilities", false, `When set, CLI will perform a Capabilities request. Usage: gnmi_cli -capabilities [-proto <gnmi.CapabilityRequest>] -address <address> [other flags ...]`)
	getFlag          = flag.Bool("get", false, `When set, CLI will perform a Get request. Usage: gnmi_cli -get -proto <gnmi.GetRequest> -address <address> [other flags ...]`)
	setFlag          = flag.Bool("set", false, `When set, CLI will perform a Set request. Usage: gnmi_cli -set -proto <gnmi.SetRequest> -address <address> [other flags ...]`)
	setReqFlag       = flag.Bool("include_set_req", false, `When set, CLI will pretty print the inputted set request`)
	withUserPass     = flag.Bool("with_user_pass", false, "When set, CLI will prompt for username/password to use when connecting to a target.")
	withPerRPCAuth   = flag.Bool("with_per_rpc_auth", false, "Use per RPC auth.")

	// Certificate files.
	insecureFlag = flag.Bool("insecure", false, "use insecure GRPC connection.")
	caCert       = flag.String("ca_crt", "", "CA certificate file. Used to verify server TLS certificate.")
	clientCert   = flag.String("client_crt", "", "Client certificate file. Used for client certificate-based authentication.")
	clientKey    = flag.String("client_key", "", "Client private key file. Used for client certificate-based authentication.")

	output_file = flag.String("output_file", "", "Output file to write the response")
	expected_cnt = flag.Uint("expected_count", 0, "End upon receiving the count of responses.")
	expected_event = flag.String("expected_event", "", "Event to capture")
	streaming_timeout = flag.Uint("streaming_timeout", 0, "Exits after this time.")

	//Subscribe Options
	streaming_type = flag.String("streaming_type", "TARGET_DEFINED", "One of TARGET_DEFINED, ON_CHANGE or SAMPLE")
	streaming_sample_int = flag.Uint("streaming_sample_interval", 0, "Streaming sample inteval seconds, 0 means lowest supported.")
	heartbeat_int = flag.Uint("heartbeat_interval", 0, "Heartbeat inteval seconds.")
	suppress_redundant = flag.Bool("suppress_redundant", false, "Suppress Redundant Subscription Updates")
)

func init() {
	flag.Var(clientTypes, "client_types", fmt.Sprintf("List of explicit client types to attempt, one of: %s.", strings.Join(client.RegisteredImpls(), ", ")))
	flag.Var(queryFlag, "query", "Comma separated list of queries.  Each query is a delimited list of OpenConfig path nodes which may also be specified as a glob (*).  The delimeter can be specified with the --delimiter flag.")
	// Query command-line flags.
	flag.Var(queryAddr, "address", "Address of the GNMI target to query.")
	flag.BoolVar(&q.UpdatesOnly, "updates_only", false, "Only stream updates, not the initial sync. Setting this flag for once or polling queries will cause nothing to be returned.")
	// Config command-line flags.
	flag.DurationVar(&cfg.PollingInterval, "polling_interval", 30*time.Second, "Interval at which to poll in seconds if polling is specified for query_type.")
	flag.UintVar(&cfg.Count, "count", 0, "Number of polling/streaming events (0 is infinite).")
	flag.StringVar(&cfg.Delimiter, "delimiter", "/", "Delimiter between path nodes in query. Must be a single UTF-8 code point.")
	flag.DurationVar(&cfg.StreamingDuration, "streaming_duration", 0, "Length of time to collect streaming queries (0 is infinite).")
	flag.StringVar(&cfg.DisplayPrefix, "display_prefix", "", "Per output line prefix.")
	flag.StringVar(&cfg.DisplayIndent, "display_indent", "  ", "Output line, per nesting-level indent.")
	flag.StringVar(&cfg.DisplayType, "display_type", "group", "Display output type (g, group, s, single, p, proto, gh, graph).")
	flag.StringVar(&q.Target, "target", "", "Name of the gNMI target.")
	flag.DurationVar(&q.Timeout, "timeout", 30*time.Second, "Terminate query if no RPC is established within the timeout duration.")
	flag.StringVar(&cfg.Timestamp, "timestamp", "", "Specify timestamp formatting in output.  One of (<empty string>, on, raw, <FORMAT>) where <empty string> is disabled, on is human readable, raw is int64 nanos since epoch, and <FORMAT> is according to golang time.Format(<FORMAT>)")
	flag.BoolVar(&cfg.DisplaySize, "display_size", false, "Display the total size of query response.")
	flag.BoolVar(&cfg.Latency, "latency", false, "Display the latency for receiving each update (Now - update timestamp).")
	flag.UintVar(&cfg.Concurrent, "concurrent", 1, "Number of concurrent client connections to serve, for graph display type only.")
	flag.UintVar(&cfg.ConcurrentMax, "concurrent_max", 1, "Double number of concurrent client connections until ConcurrentMax reached.")

	flag.StringVar(&q.TLS.ServerName, "server_name", "", "When set, CLI will use this hostname to verify server certificate during TLS handshake.")
	flag.BoolVar(&q.TLS.InsecureSkipVerify, "tls_skip_verify", false, "When set, CLI will not verify the server certificate during TLS handshake.")

	// Shortcut flags that can be used in place of the longform flags above.
	flag.Var(queryAddr, "a", "Short for address.")
	flag.Var(queryFlag, "q", "Short for query.")
	flag.StringVar(&q.Target, "t", q.Target, "Short for target.")
	flag.BoolVar(&q.UpdatesOnly, "u", q.UpdatesOnly, "Short for updates_only.")
	flag.UintVar(&cfg.Count, "c", cfg.Count, "Short for count.")
	flag.StringVar(&cfg.Delimiter, "d", cfg.Delimiter, "Short for delimiter.")
	flag.StringVar(&cfg.Timestamp, "ts", cfg.Timestamp, "Short for timestamp.")
	flag.StringVar(queryType, "qt", *queryType, "Short for query_type.")
	flag.StringVar(&cfg.DisplayType, "dt", cfg.DisplayType, "Short for display_type.")
	flag.DurationVar(&cfg.StreamingDuration, "sd", cfg.StreamingDuration, "Short for streaming_duration.")
	flag.DurationVar(&cfg.PollingInterval, "pi", cfg.PollingInterval, "Short for polling_interval.")
	flag.BoolVar(&cfg.DisplaySize, "ds", cfg.DisplaySize, "Short for display_size.")
	flag.BoolVar(&cfg.Latency, "l", cfg.Latency, "Short for latency.")
	flag.StringVar(reqProto, "p", *reqProto, "Short for request proto.")
}

func main() {
	flag.Parse()

	defer func() {
		displayHandle.Write([]byte("\n]\n"))
		displayHandle.Close()
	}()

	if len(*output_file) != 0 {
		var err error
		displayHandle, err = os.OpenFile(*output_file, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Error(fmt.Printf("unable to create output file(%v) err=%v\n", *output_file, err))
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())

	// Terminate immediately on Ctrl+C, skipping lame-duck mode.
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		cancel()
	}()

	go func() {
		if *streaming_timeout > 0 {
			var sleep_cnt uint = 0
			for sleep_cnt < *streaming_timeout {
				time.Sleep(time.Second)
				sleep_cnt += 1
				if *expected_cnt <= rcvd_cnt {
					s := fmt.Sprintf("Received all. expected:%d rcvd:%d", *expected_cnt, rcvd_cnt)
					log.V(7).Infof("Writing to terminate: %v", s)
					term <- s
					return
				}
			}
			s := fmt.Sprintf("Timeout %d Secs", *streaming_timeout)
			log.V(7).Infof("Writing to terminate: %v", s)
			term <- s
		}
	}()

	go func() {
		// Terminate when indicated.
		m := <-term
		log.V(1).Infof("Terminating due to %v", m)
		cancel()
	}()

	if len(q.Addrs) == 0 {
		log.Exit("--address must be set")
	}
	if *withUserPass {
		var err error
		q.Credentials, err = readCredentials()
		if err != nil {
			log.Exit(err)
		}
	}

	if *caCert != "" {
		certPool := x509.NewCertPool()
		ca, err := ioutil.ReadFile(*caCert)
		if err != nil {
			log.Exitf("could not read %q: %s", *caCert, err)
		}
		if ok := certPool.AppendCertsFromPEM(ca); !ok {
			log.Exit("failed to append CA certificates")
		}

		q.TLS.RootCAs = certPool
	}

	if *clientCert != "" || *clientKey != "" {
		if *clientCert == "" || *clientKey == "" {
			log.Exit("--client_crt and --client_key must be set with file locations")
		}
		certificate, err := tls.LoadX509KeyPair(*clientCert, *clientKey)
		if err != nil {
			log.Exitf("could not load client key pair: %s", err)
		}

		q.TLS.Certificates = []tls.Certificate{certificate}
	}

	if *insecureFlag {
		q.TLS = nil
	}

	var err error
	switch {
	case *capabilitiesFlag: // gnmi.Capabilities
		err = executeCapabilities(ctx)
	case *setFlag: // gnmi.Set
		err = executeSet(ctx)
	case *getFlag: // gnmi.Get
		err = executeGet(ctx)
	default: // gnmi.Subscribe
		err = executeSubscribe(ctx)
	}
	if err != nil {
		cfg.Display([]byte(fmt.Sprintf("%v", err)))
		os.Exit(1)
	}
}

func executeCapabilities(ctx context.Context) error {
	s, err := protoRequestFromFlags()
	if err != nil {
		return err
	}
	r := &gpb.CapabilityRequest{}
	if err := prototext.Unmarshal([]byte(s), r); err != nil {
		return fmt.Errorf("unable to parse gnmi.CapabilityRequest from %q : %v", s, err)
	}
	c, err := gclient.New(ctx, client.Destination{
		Addrs:       q.Addrs,
		Target:      q.Target,
		Timeout:     q.Timeout,
		Credentials: q.Credentials,
		TLS:         q.TLS,
	})
	if err != nil {
		return fmt.Errorf("could not create a gNMI client: %v", err)
	}
	response, err := c.(*gclient.Client).Capabilities(ctx, r)
	if err != nil {
		return fmt.Errorf("target returned RPC error for Capabilities(%q): %v", r.String(), err)
	}
	cfg.Display([]byte(prototext.Format(response)))
	return nil
}

func executeGet(ctx context.Context) error {
	s, err := protoRequestFromFlags()
	if err != nil {
		return err
	}
	if s == "" {
		return errors.New("-proto must be set or -proto_file must contain proto")
	}
	r := &gpb.GetRequest{}
	if err := prototext.Unmarshal([]byte(s), r); err != nil {
		return fmt.Errorf("unable to parse gnmi.GetRequest from %q : %v", s, err)
	}
	c, err := gclient.New(ctx, client.Destination{
		Addrs:       q.Addrs,
		Target:      q.Target,
		Timeout:     q.Timeout,
		Credentials: q.Credentials,
		TLS:         q.TLS,
	})
	if err != nil {
		return fmt.Errorf("could not create a gNMI client: %v", err)
	}
	response, err := c.(*gclient.Client).Get(ctx, r)
	if err != nil {
		return fmt.Errorf("target returned RPC error for Get(%q): %v", r.String(), err)
	}
	cfg.Display([]byte(prototext.Format(response)))
	return nil
}

func executeSet(ctx context.Context) error {
	s, err := protoRequestFromFlags()
	if err != nil {
		return err
	}
	if s == "" {
		return errors.New("-proto must be set or -proto_file must contain proto")
	}
	r := &gpb.SetRequest{}
	if err := prototext.Unmarshal([]byte(s), r); err != nil {
		return fmt.Errorf("unable to parse gnmi.SetRequest from %q : %v", s, err)
	}
	if *setReqFlag {
		cfg.Display([]byte(prototext.Format(r)))
	}
	c, err := gclient.New(ctx, client.Destination{
		Addrs:       q.Addrs,
		Target:      q.Target,
		Timeout:     q.Timeout,
		Credentials: q.Credentials,
		TLS:         q.TLS,
	})
	if err != nil {
		return fmt.Errorf("could not create a gNMI client: %v", err)
	}
	response, err := c.(*gclient.Client).Set(ctx, r)
	if err != nil {
		return fmt.Errorf("failed to apply Set: %w", err)
	}
	cfg.Display([]byte(prototext.Format(response)))
	return nil
}

func executeSubscribe(ctx context.Context) error {
	s, err := protoRequestFromFlags()
	if err != nil {
		return err
	}
	if s != "" {
		// Convert SubscribeRequest to a client.Query
		tq, err := cli.ParseSubscribeProto(*reqProto)
		if err != nil {
			log.Exitf("failed to parse gNMI SubscribeRequest text proto: %v", err)
		}
		// Set the fields that are not set as part of conversion above.
		tq.Addrs = q.Addrs
		tq.Credentials = q.Credentials
		tq.Timeout = q.Timeout
		tq.TLS = q.TLS
		return cli.QueryDisplay(ctx, tq, &cfg)
	}

	if q.Type = cli.QueryType(*queryType); q.Type == client.Unknown {
		return errors.New("--query_type must be one of: (o, once, p, polling, s, streaming)")
	}
	// Parse queryFlag into appropriate format.
	if len(*queryFlag) == 0 {
		return errors.New("--query must be set")
	}
	if *streaming_type == "TARGET_DEFINED" {
		q.Streaming_type = gpb.SubscriptionMode(0)
	} else if *streaming_type ==  "ON_CHANGE" {
		q.Streaming_type =  gpb.SubscriptionMode(1)
	} else if *streaming_type ==  "SAMPLE" {
		q.Streaming_type = gpb.SubscriptionMode(2)
	} else {
		return errors.New("-streaming_type must be one of: (TARGET_DEFINED, ON_CHANGE, SAMPLE)")
	}
	q.Streaming_sample_int = uint64(*streaming_sample_int)*uint64(time.Second)
	if *queryType == "streaming" || *queryType == "s" {
		q.Heartbeat_int = uint64(*heartbeat_int)*uint64(time.Second)
	} else if *heartbeat_int > 0  {
		return errors.New("-heartbeat_interval only valid with streaming query type")
	}
	q.Suppress_redundant = bool(*suppress_redundant)
	for _, path := range *queryFlag {
		query, err := parseQuery(path, cfg.Delimiter)
		if err != nil {
			return fmt.Errorf("invalid query %q : %v", path, err)
		}
		q.Queries = append(q.Queries, query)
	}
	return cli.QueryDisplay(ctx, q, &cfg)
}

func readCredentials() (*client.Credentials, error) {
	c := &client.Credentials{}
	user := os.Getenv("GNMI_USER")
	pass := os.Getenv("GNMI_PASS")
	if user != "" && pass != "" {
		c.Username = user
		c.Password = pass
		return c, nil
	}
	fmt.Print("username: ")
	_, err := fmt.Scan(&c.Username)
	if err != nil {
		return nil, err
	}

	fmt.Print("password: ")
	pb, err := terminal.ReadPassword(int(os.Stdin.Fd()))
	fmt.Print("\n") // Echo 'Enter' key.
	if err != nil {
		return nil, err
	}
	c.Password = string(pb)

	return c, nil
}

func parseQuery(query, delim string) ([]string, error) {
	d, w := utf8.DecodeRuneInString(delim)
	if w == 0 || w != len(delim) {
		return nil, fmt.Errorf("delimiter must be single UTF-8 codepoint: %q", delim)
	}
	// Ignore leading and trailing delimters.
	query = strings.Trim(query, delim)
	// Split path on delimeter with contextually aware key/value handling.
	var buf []rune
	inKey := false
	null := rune(0)
	for _, r := range query {
		switch r {
		case '[':
			if inKey {
				return nil, fmt.Errorf("malformed query, nested '[': %q ", query)
			}
			inKey = true
		case ']':
			if !inKey {
				return nil, fmt.Errorf("malformed query, unmatched ']': %q", query)
			}
			inKey = false
		case d:
			if !inKey {
				buf = append(buf, null)
				continue
			}
		}
		buf = append(buf, r)
	}
	if inKey {
		return nil, fmt.Errorf("malformed query, missing trailing ']': %q", query)
	}
	return strings.Split(string(buf), string(null)), nil
}

func protoRequestFromFlags() (string, error) {
	if *protoFile != "" {
		if *reqProto != "" {
			return "", errors.New("only one of -proto and -proto_file are allowed to be set")
		}
		b, err := os.ReadFile(*protoFile)
		if err != nil {
			return "", fmt.Errorf("could not read %q: %v", *protoFile, err)
		}
		return string(b), nil
	}
	return *reqProto, nil
}
