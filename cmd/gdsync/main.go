package main

import (
	"encoding/gob"
	"fmt"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"net/http"
	"strings"
	"time"
	"code.google.com/p/google-api-go-client/drive/v2"
	"code.google.com/p/goauth2/oauth"
)

// NullWriter is a writer to empty.
type NullWriter struct {
}

func (n *NullWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

// Settings for authorization.
var config = &oauth.Config{
	ClientId:     "951649296996.apps.googleusercontent.com",
	ClientSecret: "Sa2IG-pAo0hBzquJfbc5aew-",
	Scope:        drive.DriveScope,
	RedirectURL:  "urn:ietf:wg:oauth:2.0:oob",
	AuthURL:      "https://accounts.google.com/o/oauth2/auth",
	TokenURL:     "https://accounts.google.com/o/oauth2/token",
	// AccessType should be offline if you want to save the token and reuse it in the future.
	// AccessType:   "offline",
}

var authfile string
var verbose bool
var msg *log.Logger

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

func GetToplevelEntry(svc *drive.Service, name string) (*drive.File, error) {
	flist, err := svc.Files.List().Do()
	if err != nil {
		fmt.Printf("Cannot get the file list: %v\n", err)
		return nil, err
	}
	for {
		for _, f := range flist.Items {
			if f.Title == name {
				return f, nil
			}
		}
		if flist.NextPageToken == "" {
			break
		}
		flist, err = svc.Files.List().PageToken(flist.NextPageToken).Do()
		if err != nil {
			fmt.Printf("Cannot get the file list: %v\n", err)
			return nil, err
		}
	}
	return nil, os.ErrNotExist
}

func GetEntry(svc *drive.Service, file *drive.File, paths []string) (*drive.File, error) {
	if len(paths) == 0 {
		return file, nil
	}

	clist, err := svc.Children.List(file.Id).Do()
	for {
		if err != nil {
			fmt.Printf("Failed to get the child list: %v\n", err)
			return nil, err
		}
		for _, child := range clist.Items {
			cfile, err := svc.Files.Get(child.Id).Do()
			if err != nil {
				fmt.Printf("Cannot get the file info: %v\n", err)
				continue
			}
			if file.Title == paths[0] {
				return GetEntry(svc, cfile, paths[1:])
			}
		}
		if clist.NextPageToken != "" {
			clist, err = svc.Children.List(file.Id).PageToken(clist.NextPageToken).Do()
		} else {
			break
		}
	}
	return nil, os.ErrNotExist
}

func DownloadFilesTo(svc *drive.Service, t *oauth.Transport, file *drive.File, dst string) {
	dstfile := filepath.Join(dst, file.Title)
	srcmtime, err := time.Parse(time.RFC3339, file.ModifiedDate)
	if err != nil {
		fmt.Printf("Cannot parse the time: %v\n", file.ModifiedDate)
		return
	}
	finfo, err := os.Stat(dstfile)
	if err == nil {
		// Check the existence and/or updates.
		if file.MimeType == "application/vnd.google-apps.folder" && !finfo.IsDir() {
			os.Remove(dstfile)
		} else if file.MimeType != "application/vnd.google-apps.folder" && finfo.IsDir() {
			os.RemoveAll(dstfile)
		} else if !finfo.IsDir() && (srcmtime.Equal(finfo.ModTime()) || srcmtime.Before(finfo.ModTime())) {
			msg.Printf("Skipping: %v\n", file.Title)
			return
		}
	}
	if file.MimeType == "application/vnd.google-apps.folder" {
		msg.Printf("Syncing %v\n", file.Title)
		if err != nil {
			os.Mkdir(dstfile, 0777)
		}
		clist, err := svc.Children.List(file.Id).Do()
		for {
			if err != nil {
				fmt.Printf("Cannot get the child list: %v\n", err)
				break
			}
			for _, child := range clist.Items {
				cfile, err := svc.Files.Get(child.Id).Do()
				if err != nil {
					fmt.Printf("Cannot get the file info: %v\n", err)
					continue
				}
				DownloadFilesTo(svc, t, cfile, dstfile)
			}
			if clist.NextPageToken == "" {
				break
			} else {
				clist, err = svc.Children.List(file.Id).PageToken(clist.NextPageToken).Do()
			}
		}
	} else if file.DownloadUrl != "" {
		msg.Printf("Download %v\n", file.Title)
		dsthandle, err := os.Create(dstfile)
		defer dsthandle.Close()
		if err != nil {
			fmt.Printf("Cannot open a new file: %v\n", dstfile)
			return
		}
		req, err := http.NewRequest("GET", file.DownloadUrl, nil)
		if err != nil {
			fmt.Printf("Cannot create a new request: %v\n", err)
			return
		}
		resp, err := t.RoundTrip(req)
		defer resp.Body.Close()
		if err != nil {
			fmt.Printf("Error happens for downloading %v: %v\n", file, err)
			return
		}
		io.Copy(dsthandle, resp.Body)
		dsthandle.Sync()
		os.Chtimes(dstfile, srcmtime, srcmtime)
	} else {
		fmt.Printf("Cannot download file: %v\n", file)
	}
}

func CreateDirectoryIfMissing(svc *drive.Service, file *drive.File, name string) (*drive.File, error) {
	if file != nil {
		clist, err := svc.Children.List(file.Id).Do()
		if err != nil {
			fmt.Printf("Cannot get the list: %v\n", clist)
		}
		for _, child := range clist.Items {
			cfile, err := svc.Files.Get(child.Id).Do()
			if err != nil {
				fmt.Printf("Cannot get the file: %s\n", child.Id)
				continue
			}
			if cfile.Title == name {
				return cfile, nil
			}
		}
	}
	msg.Printf("Creating a folder...")
	folder := &drive.File {
		Title: name,
		MimeType: "application/vnd.google-apps.folder",
	}
	if file != nil {
		parent := &drive.ParentReference{
			Id: file.Id,
		}
		folder.Parents = []*drive.ParentReference{parent}
	}
	return svc.Files.Insert(folder).Do()
}

func UploadFilesTo(svc *drive.Service, src string, parent *drive.ParentReference) {
	finfo, err := os.Stat(src)
	if err != nil {
		fmt.Printf("Cannot open the file: %s\n", src)
		return
	}
	var basedir string
	var names []string
	if finfo.IsDir() {
		srchandle, err := os.Open(src)
		names, err = srchandle.Readdirnames(0)
		if err != nil {
			fmt.Printf("Cannot read the directory: %v\n", err)
			return
		}
		basedir = src
	} else {
		var name string
		basedir, name = filepath.Split(src)
		names = []string{name}
	}

	drivefiles := make(map[string]*drive.File)
	if (parent != nil) {
		clist, err := svc.Children.List(parent.Id).Do()
		for {
			if err != nil {
				fmt.Printf("Cannot get the child list: %v\n", err)
				break
			}
			for _, child := range clist.Items {
				cfile, err := svc.Files.Get(child.Id).Do()
				if err != nil {
					fmt.Printf("Cannot get th file info: %v\n", err)
					continue
				}
				drivefiles[cfile.Title] = cfile
			}
			if (clist.NextPageToken != "") {
				clist, err = svc.Children.List(parent.Id).PageToken(clist.NextPageToken).Do()
			} else {
				break
			}
		}
	}

	for _, name := range names {
		file, err := os.Open(filepath.Join(basedir, name))
		if err != nil {
			fmt.Printf("Cannot open the directory: %v\n", name)
			continue
		}
		each_finfo, err := file.Stat()
		each_mtime := each_finfo.ModTime()
		if err != nil {
			fmt.Printf("Cannot stat: %v\n", err)
			continue
		}
		var updateId string
		if drivefiles[name] != nil {
			drivefile := drivefiles[name] 
			mtime, err := time.Parse(time.RFC3339, drivefile.ModifiedDate)
			if err == nil && (each_mtime.Equal(mtime) || each_mtime.Before(mtime)) {
				msg.Printf("Skipping %s\n", name)
				continue
			}
			updateId = drivefile.Id
		}
		drivefile := &drive.File {
			Title: name,
		}
		if each_finfo.IsDir() {
			drivefile.MimeType = "application/vnd.google-apps.folder"
		}
		if (parent != nil) {
			drivefile.Parents = []*drive.ParentReference{parent}
		}
		var result *drive.File
		if updateId != "" {
			call := svc.Files.Update(updateId, drivefile)
			if !each_finfo.IsDir() {
				call = call.Media(file)
			}
			result, err = call.Do()
			if err != nil {
				fmt.Printf("Failed to update: %v\n", err)
				continue
			}
		} else {
			call := svc.Files.Insert(drivefile)
			if !each_finfo.IsDir() {
				call = call.Media(file)
			}
			result, err = call.Do()
			if err != nil {
				fmt.Printf("Failed to insert: %v\n", err)
				continue
			}
		}

		if each_finfo.IsDir() && result != nil {
			newParent := &drive.ParentReference{
				Id: result.Id,
			}
			msg.Printf("Syncing %s\n", name)
			UploadFilesTo(svc, filepath.Join(basedir, name), newParent)
		} else {
			msg.Printf("Uploaded: %s\n", name)
		}
	}
}

func DoSync(svc *drive.Service, t *oauth.Transport, src string, dst string) {
	if strings.HasPrefix(src, "drive:") {
		msg.Printf("Downloading files...")
		if src[6:] == "" {
			fmt.Printf("Do not sync the drive root.")
			return
		}
		paths := filepath.SplitList(src[6:])
		file, err := GetToplevelEntry(svc, paths[0])
		if err != nil {
			return
		}
		file, err = GetEntry(svc, file, paths[1:])
		if err != nil {
			return
		}
		DownloadFilesTo(svc, t, file, dst)
	} else if strings.HasPrefix(dst, "drive:") {
		msg.Printf("Uploading files...")
		dst = dst[6:]
		var parent *drive.ParentReference
		if dst != "" {
			paths := filepath.SplitList(dst)
			file, err := GetToplevelEntry(svc, paths[0])
			if err == os.ErrNotExist && len(paths) == 1 {
				file, err = CreateDirectoryIfMissing(svc, nil, paths[0])
				if err != nil {
					fmt.Printf("Cannot create the directory: %v\n", err)
					return
				}
			} else if err != nil {
				fmt.Printf("Cannot find the directory: %v\n", err)
				return
			} else if len(paths) > 1 {
				file, err = GetEntry(svc, file, paths[1:len(paths)-1])
				if err != nil {
					return
				}
				file, err = CreateDirectoryIfMissing(svc, file, paths[len(paths)-1])
				if err != nil {
					fmt.Printf("Cannot create the directory: %v\n", err)
					return
				}
			}
			if file == nil || file.MimeType != "application/vnd.google-apps.folder" {
				fmt.Printf("target is not a folder")
				return
			}
			parent = &drive.ParentReference {
				Id: file.Id,
			}
		}
		UploadFilesTo(svc, src, parent)
	} else {
		fmt.Printf("Both source and destination are local. Quitting...\n")
	}
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

	if verbose {
		msg = log.New(os.Stdout, "", 0)
	} else {
		msg = log.New(&NullWriter{}, "", 0)
	}

	t := initAuthToken()

	// Create a new authorized Drive client.
	svc, err := drive.New(t.Client())
	if err != nil {
		fmt.Printf("Cannot start drive service: %v\n", err)
		return
	}

	DoSync(svc, t, src, dst)
}
