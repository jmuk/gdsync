package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"flag"
	"log"
	"os"
	"path"
	"strings"
	"github.com/jmuk/gdsync"
	"github.com/gcmurphy/getpass"
)

var exclude string
var excludeFrom string
var clientSettings string
var authfile string
var passphrase string
var verbose bool
var doDelete bool
var useTextPlain bool

func initFlags() {
	client_default := path.Join(os.Getenv("HOME"), ".gdsync_client")
	flag.StringVar(&clientSettings, "client", client_default, "The file name to store the oauth clients in JSON.")
	flag.StringVar(&authfile, "authfile", "", "Store/restore the oauth authentication information")
	flag.StringVar(&passphrase, "P", "\000", "The passphrase to store authfile")
	flag.BoolVar(&verbose, "v", false, "Verbose output")
	flag.StringVar(&exclude, "exclude", "", "Specify the exclude pattern")
	flag.StringVar(&excludeFrom, "exclude-from", "", "Specify the file of exclude patterns")
	flag.BoolVar(&doDelete, "delete", false, "delete missing files if specified")
	flag.BoolVar(&useTextPlain, "use-text-plain", false, "Use text/plain mime-type for text-like files rather than specific type such like text/html")
	flag.Parse()
}

// Uploads a file to Google Drive
func main() {
	initFlags()

	if flag.NArg() != 2 {
		fmt.Printf("Usage: %s SRC DST\n", os.Args[0])
		fmt.Printf("SRC or DST must start with drive: prefix")
		flag.PrintDefaults()
		return
	}

	src := flag.Arg(0)
	dst := flag.Arg(1)

	client_file, err := os.Open(clientSettings)
	if err != nil {
		fmt.Printf("Failed to load the client settings: %v\n", err)
		return
	}
	defer client_file.Close()
	var clientData map[string]string
	if err := json.NewDecoder(client_file).Decode(&clientData); err != nil {
		fmt.Printf("Failed to parse the client settings: %v\n", err)
		return
	}
	clientId, id_ok := clientData["ClientId"]
	clientSecret, secret_ok := clientData["ClientSecret"]
	if !id_ok || !secret_ok {
		fmt.Printf("Cannot find value for ClientId or ClientSecret")
		return
	}
	config := gdsync.GetAuthConfig(clientId, clientSecret)
	

	if authfile != "" && passphrase == "\000" {
		var err error
		passphrase, err = getpass.GetPassWithOptions("Enter the passphrase: ", 0, getpass.DefaultMaxPass)
		if err != nil {
			fmt.Printf("Failed to read the passphrase: %v\n", err)
			return
		}
	}

	token_manager, err := gdsync.NewTokenManager(config, authfile, passphrase)
	if err != nil {
		fmt.Printf("Failed to initialize the auth token: %v\n", err)
		return
	}

	// Create a new authorized Drive client.
	syncer, err := gdsync.NewGDSyncer(token_manager.Transport)
	if err != nil {
		fmt.Printf("Cannot start drive service: %v\n", err)
		return
	}

	syncer.SetErrorLogger(log.New(os.Stderr, "", 0))
	if verbose {
		syncer.SetLogger(log.New(os.Stdout, "", 0))
	}

	if exclude != "" {
		syncer.AddExcludePattern(exclude)
	}
	if excludeFrom != "" {
		if exclude_file, err := os.Open(excludeFrom); err == nil {
			fmt.Printf("reading from %s...\n", excludeFrom)
			bufreader := bufio.NewReader(exclude_file)
			for {
				if pattern, err := bufreader.ReadString('\n'); err == nil {
					syncer.AddExcludePattern(strings.TrimSpace(pattern))
				} else {
					syncer.AddExcludePattern(strings.TrimSpace(pattern))
					break
				}
			}
		} else {
			fmt.Printf("Cannot read the file %s: %v\n", excludeFrom, err)
		}
	}

	if doDelete {
		syncer.DoDelete()
	}
	if useTextPlain {
		syncer.UseTextPlain()
	}

	syncer.DoSync(src, dst)
}
