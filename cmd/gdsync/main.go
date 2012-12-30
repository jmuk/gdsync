package main

import (
	"encoding/json"
	"fmt"
	"flag"
	"log"
	"os"
	"path"
	"github.com/jmuk/gdsync"
	"github.com/gcmurphy/getpass"
)

var clientSettings string
var authfile string
var passphrase string
var verbose bool

func initFlags() {
	client_default := path.Join(os.Getenv("HOME"), ".gdsync_client")
	flag.StringVar(&clientSettings, "client", client_default, "The file name to store the oauth clients in JSON.")
	flag.StringVar(&authfile, "authfile", "", "Store/restore the oauth authentication information")
	flag.StringVar(&passphrase, "P", "\000", "The passphrase to store authfile")
	flag.BoolVar(&verbose, "v", false, "Verbose output")
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

	syncer.DoSync(src, dst)
}
