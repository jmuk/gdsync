package gdsync

import (
	"encoding/gob"
	"fmt"
	"net/http"
	"os"
	"time"
	"code.google.com/p/goauth2/oauth"
	)

type TokenManager struct {
	Transport *oauth.Transport
	Config *oauth.Config
	Filename string
}

func NewTokenManager(config *oauth.Config, filename, passphrase string) (*TokenManager, error) {
	if filename != "" {
		config.AccessType = "offline"
	}
	mgr := &TokenManager{
		Config: config,
		Filename: filename,
	}
	err := mgr.initialize(passphrase)
	if err != nil {
		return nil, err
	}
	return mgr, nil
}

func (mgr *TokenManager) recoverFromStoredToken(passphrase string) error {
	if mgr.Filename == "" {
		return os.ErrNotExist
	}
	fin, err := NewProtectedFileReader(mgr.Filename, passphrase)
	if err != nil {
		return err
	}
	defer fin.File.Close()
	var token *oauth.Token = nil
	if err := gob.NewDecoder(fin).Decode(&token); err != nil {
		return err
	}
	t := &oauth.Transport {
		Config:    mgr.Config,
		Token:     token,
		Transport: http.DefaultTransport,
	}
	if !token.Expiry.IsZero() && token.Expiry.Before(time.Now()) {
		err := t.Refresh()
		if (err != nil) {
			return err
		}
		fout, err := NewProtectedFileWriter(mgr.Filename, passphrase)
		if err == nil {
			gob.NewEncoder(fout).Encode(token)
			fout.File.Close()
		}
	}
	mgr.Transport = t
	return nil
}

func (mgr *TokenManager) initialize(passphrase string) error {
	if err := mgr.recoverFromStoredToken(passphrase); err == nil {
		return nil
	}

	// Generate a URL to visit for authorization.
	authUrl := mgr.Config.AuthCodeURL("state")
	fmt.Printf("Go to the following link in your browser: %v\n", authUrl)
	t := &oauth.Transport{
		Config:    mgr.Config,
		Transport: http.DefaultTransport,
	}

	// Read the code, and exchange it for a token.
	fmt.Printf("Enter verification code: ")
	var code string
	fmt.Scanln(&code)
	_, err := t.Exchange(code)
	if err != nil {
		return err
	}

	mgr.Transport = t
	if mgr.Filename != "" {
		fout, err := NewProtectedFileWriter(mgr.Filename, passphrase)
		if err != nil {
			fmt.Printf("An error occured storing the auth file: %v\n", err)
		} else {
			fout.File.Chmod(0600)
			gob.NewEncoder(fout).Encode(t.Token)
			fout.File.Close()
		}
	}
	return nil
}

func init() {
	gob.Register(oauth.Token{})
}