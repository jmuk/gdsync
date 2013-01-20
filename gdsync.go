package gdsync

import (
	"io"
	"log"
	"mime"
	"os"
	"path/filepath"
	"net/http"
	"strings"
	"time"
	"code.google.com/p/google-api-go-client/drive/v2"
	"code.google.com/p/goauth2/oauth"
)

type nullWriter struct {
}

func (n *nullWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func nullLogger() *log.Logger {
	return log.New(&nullWriter{}, "", 0)
}

func GetAuthConfig(clientId, clientSecret string) *oauth.Config {
	return &oauth.Config {
		ClientId: clientId,
		ClientSecret: clientSecret,
		Scope:        drive.DriveScope,
		RedirectURL:  "urn:ietf:wg:oauth:2.0:oob",
		AuthURL:      "https://accounts.google.com/o/oauth2/auth",
		TokenURL:     "https://accounts.google.com/o/oauth2/token",
	}
}

type gdSyncerOptions struct {
	excludePatterns []string
	doDelete bool
	useTextPlain bool
}

type GDSyncer struct {
	svc *drive.Service
	transport *oauth.Transport
	msg *log.Logger
	err *log.Logger
	gdSyncerOptions
}

func NewGDSyncer(t *oauth.Transport) (*GDSyncer, error) {
	service, err := drive.New(t.Client())
	if err != nil {
		return nil, err
	}

	return &GDSyncer{
		svc: service,
		transport: t,
		msg: nullLogger(),
		err: nullLogger(),
	}, nil
}

func (s *GDSyncer) SetLogger(logger *log.Logger) {
	s.msg = logger
}

func (s *GDSyncer) SetErrorLogger(logger *log.Logger) {
	s.err = logger
}

func (s *GDSyncer) DoDelete() {
	s.doDelete = true
}

func (s *GDSyncer) UseTextPlain() {
	s.useTextPlain = true
}

func (s *GDSyncer) AddExcludePattern(pattern string) {
	if pattern != "" {
		s.excludePatterns = append(s.excludePatterns, pattern)
		s.msg.Printf("Added an exclude pattern: %s\n", pattern)
	}
}

func (s *GDSyncer) ShouldExcludeName(name string) bool {
	// TODO: allow directory pattern ("dir/fpat.*"-style patterns)
	for _, pattern := range s.excludePatterns {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

func buildQuery(name string) string {
	return "title='" + strings.Replace(name, "'", "\\'", -1) + "'"
}

func (s *GDSyncer) getToplevelEntry(name string) (*drive.File, error) {
	flist, err := s.svc.Files.List().Q(buildQuery(name)).Do()
	if err != nil {
		s.err.Printf("Cannot get the file list: %v\n", err)
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
		flist, err = s.svc.Files.List().PageToken(flist.NextPageToken).Do()
		if err != nil {
			s.err.Printf("Cannot get the file list: %v\n", err)
			return nil, err
		}
	}
	return nil, os.ErrNotExist
}

func (s *GDSyncer) getEntry(file *drive.File, paths []string) (*drive.File, error) {
	if len(paths) == 0 {
		return file, nil
	}

	clist, err := s.svc.Children.List(file.Id).Q(buildQuery(paths[0])).Do()
	for {
		if err != nil {
			s.err.Printf("Failed to get the child list: %v\n", err)
			return nil, err
		}
		for _, child := range clist.Items {
			cfile, err := s.svc.Files.Get(child.Id).Do()
			if err != nil {
				s.err.Printf("Cannot get the file info: %v\n", err)
				continue
			}
			if file.Title == paths[0] {
				return s.getEntry(cfile, paths[1:])
			}
		}
		if clist.NextPageToken != "" {
			clist, err = s.svc.Children.List(file.Id).PageToken(clist.NextPageToken).Do()
		} else {
			break
		}
	}
	return nil, os.ErrNotExist
}

func (s *GDSyncer) downloadFilesTo(file *drive.File, dst string) {
	if s.ShouldExcludeName(file.Title) {
		s.msg.Printf("Skipping: %s\n", file.Title)
		return
	}

	dstfile := filepath.Join(dst, file.Title)
	srcmtime, err := time.Parse(time.RFC3339, file.ModifiedDate)
	if err != nil {
		s.err.Printf("Cannot parse the time: %v\n", file.ModifiedDate)
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
			s.msg.Printf("Skipping: %v\n", file.Title)
			return
		}
	}
	if file.MimeType == "application/vnd.google-apps.folder" {
		s.msg.Printf("Syncing %v\n", dstfile)
		local_files := make(map[string]bool)
		if err != nil {
			os.Mkdir(dstfile, 0777)
		} else {
			dsthandle, _ := os.Open(dstfile)
			names, _ := dsthandle.Readdirnames(0)
			for _, name := range names {
				local_files[name] = true
			}
			dsthandle.Close()
		}
		clist, err := s.svc.Children.List(file.Id).Do()
		for {
			if err != nil {
				s.err.Printf("Cannot get the child list: %v\n", err)
				break
			}
			for _, child := range clist.Items {
				cfile, err := s.svc.Files.Get(child.Id).Do()
				if err != nil {
					s.err.Printf("Cannot get the file info: %v\n", err)
					continue
				}
				delete(local_files, cfile.Title)
				s.downloadFilesTo(cfile, dstfile)
			}
			if clist.NextPageToken == "" {
				break
			} else {
				clist, err = s.svc.Children.List(file.Id).PageToken(clist.NextPageToken).Do()
			}
		}
		if s.doDelete {
			for name, _ := range local_files {
				// Do not delete files of excluded pattern!!
				if s.ShouldExcludeName(name) {
					continue
				}
				full_path := filepath.Join(dstfile, name)
				s.msg.Printf("deleting %s", full_path)
				os.Remove(full_path)
			}
		}
	} else if file.DownloadUrl != "" {
		dsthandle, err := os.Create(dstfile)
		defer dsthandle.Close()
		if err != nil {
			s.err.Printf("Cannot open a new file: %v\n", dstfile)
			return
		}
		req, err := http.NewRequest("GET", file.DownloadUrl, nil)
		if err != nil {
			s.err.Printf("Cannot create a new request: %v\n", err)
			return
		}
		resp, err := s.transport.RoundTrip(req)
		defer resp.Body.Close()
		if err != nil {
			s.err.Printf("Error happens for downloading %v: %v\n", file, err)
			return
		}
		io.Copy(dsthandle, resp.Body)
		dsthandle.Sync()
		s.msg.Printf("Downloaded %v\n", dstfile)
		os.Chtimes(dstfile, srcmtime, srcmtime)
	} else {
		s.err.Printf("Cannot download file: %v\n", file)
	}
}

func (s *GDSyncer) createDirectoryIfMissing(file *drive.File, name string) (*drive.File, error) {
	if file != nil {
		clist, err := s.svc.Children.List(file.Id).Q(buildQuery(name)).Do()
		if err != nil {
			s.err.Printf("Cannot get the list: %v\n", clist)
		}
		for _, child := range clist.Items {
			cfile, err := s.svc.Files.Get(child.Id).Do()
			if err != nil {
				s.err.Printf("Cannot get the file: %s\n", child.Id)
				continue
			}
			if cfile.Title == name {
				return cfile, nil
			}
		}
	}
	s.msg.Printf("Creating a folder...")
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
	return s.svc.Files.Insert(folder).Do()
}

func (s *GDSyncer) uploadFilesTo(src string, parent *drive.ParentReference) {
	finfo, err := os.Stat(src)
	if err != nil {
		s.err.Printf("Cannot open the file: %s\n", src)
		return
	}

	var basedir string
	var names []string
	if finfo.IsDir() {
		srchandle, err := os.Open(src)
		names, err = srchandle.Readdirnames(0)
		if err != nil {
			s.err.Printf("Cannot read the directory: %v\n", err)
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
		clist, err := s.svc.Children.List(parent.Id).Do()
		for {
			if err != nil {
				s.err.Printf("Cannot get the child list: %v\n", err)
				break
			}
			for _, child := range clist.Items {
				cfile, err := s.svc.Files.Get(child.Id).Do()
				if err != nil {
					s.err.Printf("Cannot get th file info: %v\n", err)
					continue
				}
				drivefiles[cfile.Title] = cfile
			}
			if (clist.NextPageToken != "") {
				clist, err = s.svc.Children.List(parent.Id).PageToken(clist.NextPageToken).Do()
			} else {
				break
			}
		}
	}

	for _, name := range names {
		full_path := filepath.Join(basedir, name)
		if s.ShouldExcludeName(name) {
			s.msg.Printf("Skipping: %s\n", full_path)
			delete(drivefiles, name)
			continue
		}
		file, err := os.Open(full_path)
		if err != nil {
			s.err.Printf("Cannot open the file: %v\n", full_path)
			continue
		}
		each_finfo, err := file.Stat()
		each_mtime := each_finfo.ModTime()
		if err != nil {
			s.err.Printf("Cannot stat: %v\n", err)
			continue
		}
		var updateId string
		if drivefiles[name] != nil {
			drivefile := drivefiles[name] 
			delete(drivefiles, name)
			mtime, err := time.Parse(time.RFC3339, drivefile.ModifiedDate)
			if err == nil && (each_mtime.Equal(mtime) || each_mtime.Before(mtime)) {
				s.msg.Printf("Skipping: %s\n", full_path)
				continue
			}
			updateId = drivefile.Id
		}
		drivefile := &drive.File {
			Title: name,
		}
		if each_finfo.IsDir() {
			drivefile.MimeType = "application/vnd.google-apps.folder"
		} else {
			mimeType := mime.TypeByExtension(filepath.Ext(name))
			if mimeType == "" || (s.useTextPlain && (strings.HasPrefix(mimeType, "text/") || mimeType == "application/json")) {
				mimeType = "text/plain"
			}
			drivefile.MimeType = mimeType
		}
		if (parent != nil) {
			drivefile.Parents = []*drive.ParentReference{parent}
		}
		var result *drive.File
		if updateId != "" {
			call := s.svc.Files.Update(updateId, drivefile)
			if !each_finfo.IsDir() {
				call = call.Media(file)
			}
			result, err = call.Do()
			if err != nil {
				s.err.Printf("Failed to update %s: %v\n", full_path, err)
				continue
			}
		} else {
			call := s.svc.Files.Insert(drivefile)
			if !each_finfo.IsDir() {
				call = call.Media(file)
			}
			result, err = call.Do()
			if err != nil {
				s.err.Printf("Failed to insert %s: %v\n", full_path, err)
				continue
			}
		}

		if each_finfo.IsDir() && result != nil {
			newParent := &drive.ParentReference{
				Id: result.Id,
			}
			s.err.Printf("Syncing %s\n", full_path)
			s.uploadFilesTo(full_path, newParent)
		} else {
			s.err.Printf("Uploaded: %s\n", full_path)
		}
	}
	if s.doDelete {
		for name, file := range drivefiles {
			s.msg.Printf("deleting file: %s\n", filepath.Join(basedir, name))
			s.svc.Files.Delete(file.Id).Do()
		}
	}
}

func (s *GDSyncer) DoSync(src string, dst string) {
	if strings.HasPrefix(src, "drive:") {
		s.msg.Printf("Downloading files...")
		src = src[6:]
		if src == "" {
			s.err.Printf("Do not sync the drive root.")
			return
		}
		paths := filepath.SplitList(src)
		file, err := s.getToplevelEntry(paths[0])
		if err != nil {
			return
		}
		file, err = s.getEntry(file, paths[1:])
		if err != nil {
			return
		}
		s.downloadFilesTo(file, dst)
	} else if strings.HasPrefix(dst, "drive:") {
		s.msg.Printf("Uploading files...")
		dst = dst[6:]
		_, src_base := filepath.Split(src)
		var file *drive.File
		var err error
		if dst != "" {
			paths := filepath.SplitList(dst)
			file, err = s.getToplevelEntry(paths[0])
			if err == os.ErrNotExist && len(paths) == 1 {
				file, err = s.createDirectoryIfMissing(nil, paths[0])
				if err != nil {
					s.err.Printf("Cannot create the directory: %v\n", err)
					return
				}
			} else if err != nil {
				s.err.Printf("Cannot find the directory: %v\n", err)
				return
			} else if len(paths) > 1 {
				file, err = s.getEntry(file, paths[1:len(paths)-1])
				if err != nil {
					return
				}
				file, err = s.createDirectoryIfMissing(file, paths[len(paths)-1])
				if err != nil {
					s.err.Printf("Cannot create the directory: %v\n", err)
					return
				}
			}
			if file == nil || file.MimeType != "application/vnd.google-apps.folder" {
				s.err.Printf("target is not a folder")
				return
			}
			if (src_base != ".") {
				file, err = s.createDirectoryIfMissing(file, src_base)
			}
		} else {
			file, err = s.getToplevelEntry(src_base)
			if err == os.ErrNotExist {
				file, err = s.createDirectoryIfMissing(nil, src_base)
				if err != nil {
					s.err.Printf("Cannot create the directory: %v\n", err)
					return
				}
			}
		}

		s.uploadFilesTo(src, &drive.ParentReference{Id: file.Id,})
	} else {
		s.err.Printf("Both source and destination are local. Quitting...\n")
	}
}
