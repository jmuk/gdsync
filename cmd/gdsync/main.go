package main

import (
	"encoding/gob"
	"fmt"
	"flag"
	"log"
	"os"
	"net/http"
	"time"
	"github.com/mukai/gdsync"
	"code.google.com/p/goauth2/oauth"
)

// Settings for authorization.
var config = gdsync.GetAuthConfig("951649296996.apps.googleusercontent.com", "Sa2IG-pAo0hBzquJfbc5aew-")

var authfile string
var verbose bool

func initFlags() {
	// It is not secure to store the token file directly to the local file system.
	// TODO: fix this to lock the file with a password.
	// flag.StringVar(&authfile, "authfile", "", "Store/restore the oauth authentication information")
	flag.BoolVar(&verbose, "v", false, "Verbose output")
	flag.Parse()
}

func recoverFromStoredToken() (*oauth.Transport, error) {
	if authfile == "" {
		return nil, os.ErrNotExist
	}
	fin, err := os.Open(authfile)
	if err != nil {
		fmt.Printf("Cannot open %v: %v\n", authfile, err)
		return nil, err
	}
	var token *oauth.Token = nil
	gob.NewDecoder(fin).Decode(&token)
	t := &oauth.Transport {
		Config:    config,
		Token:     token,
		Transport: http.DefaultTransport,
	}
	if !token.Expiry.IsZero() && token.Expiry.Before(time.Now()) {
		err := t.Refresh()
		if (err != nil) {
			return nil, err
		}
	}
	return t, nil
}

func initAuthToken() *oauth.Transport {
	t, err := recoverFromStoredToken()
	if err == nil {
		return t
	}

	// Generate a URL to visit for authorization.
	authUrl := config.AuthCodeURL("state")
	fmt.Printf("Go to the following link in your browser: %v\n", authUrl)
	t = &oauth.Transport{
		Config:    config,
		Transport: http.DefaultTransport,
	}

	// Read the code, and exchange it for a token.
	fmt.Printf("Enter verification code: ")
	var code string
	fmt.Scanln(&code)
	_, err2 := t.Exchange(code)
	if err2 != nil {
		fmt.Printf("An error occurred exchanging the code: %v\n", err)
	}

	if authfile != "" {
		fout, err := os.Create(authfile)
		if (err != nil) {
			fmt.Printf("An error occured storing the auth file\n")
		} else {
			fout.Chmod(0600)
			gob.NewEncoder(fout).Encode(t.Token)
		}
	}
	return t
}

// Uploads a file to Google Drive
func main() {
	initFlags()
	gob.Register(oauth.Token{})

	if flag.NArg() != 2 {
		fmt.Printf("Usage: %s SRC DST\n", os.Args[0])
		fmt.Printf("SRC or DST must start with drive: prefix")
		flag.PrintDefaults()
		return
	}

	src := flag.Arg(0)
	dst := flag.Arg(1)

	t := initAuthToken()

	// Create a new authorized Drive client.
	syncer, err := gdsync.NewGDSyncer(t)
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
