package handlers

// Download tickets for workspace files (upstream workspace_transfer.rs +
// workspace_files.rs create/consume_download_ticket). A browser/mobile client
// that needs a plain GET URL to download a file/dir first POSTs
// workspace_download_ticket to mint a short-lived ticket, then GETs
// /workspace_download/<ticket>, which streams the file or a zipped dir once and
// expires it. The ticket store is per-Router (per process); tickets live 60s.

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"yoyocode/src-go/web"
)

const downloadTicketTTL = 60 * time.Second

type downloadTicket struct {
	rootPath     string
	relativePath string
	kind         string // "file" | "dir"
	filename     string
	expiresAt    time.Time
}

type downloadTicketStore struct {
	mu      sync.Mutex
	tickets map[string]downloadTicket
	ttl     time.Duration
}

func newDownloadTicketStore() *downloadTicketStore {
	return &downloadTicketStore{tickets: map[string]downloadTicket{}, ttl: downloadTicketTTL}
}

func (s *downloadTicketStore) cleanup() {
	now := time.Now()
	for id, t := range s.tickets {
		if !now.Before(t.expiresAt) {
			delete(s.tickets, id)
		}
	}
}

func (s *downloadTicketStore) issue(t downloadTicket) (string, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanup()
	id := newChatUUID()
	t.expiresAt = time.Now().Add(s.ttl)
	s.tickets[id] = t
	return id, t.expiresAt.Unix()
}

func (s *downloadTicketStore) consume(id string) (downloadTicket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanup()
	t, ok := s.tickets[id]
	if !ok {
		return downloadTicket{}, false
	}
	delete(s.tickets, id)
	if time.Now().Before(t.expiresAt) {
		return t, true
	}
	return downloadTicket{}, false
}

// registerWorkspaceDownloadTickets wires workspace_download_ticket (POST) and
// the GET /workspace_download/<ticket> consume endpoint onto a shared store.
func registerWorkspaceDownloadTickets(reg *Registry) {
	store := newDownloadTicketStore()

	reg.Register("workspace_download_ticket", func(c *web.Context) (any, error) {
		rootPath := GetString(c, "rootPath", "root_path")
		relPath := GetString(c, "path")
		kind := strings.TrimSpace(GetString(c, "kind"))
		if kind != "file" && kind != "dir" {
			return nil, web.ErrInvalidInput("kind must be 'file' or 'dir'")
		}
		var filename string
		if kind == "file" {
			target, err := resolveDownloadFileTarget(rootPath, relPath)
			if err != nil {
				return nil, err
			}
			filename = baseName(target, "download")
		} else {
			_, dirName, err := resolveDownloadDirTarget(rootPath, relPath)
			if err != nil {
				return nil, err
			}
			filename = dirName + ".zip"
		}
		id, expiresAt := store.issue(downloadTicket{
			rootPath: rootPath, relativePath: relPath, kind: kind, filename: filename,
		})
		return map[string]any{
			"ticket":    id,
			"url":       "/workspace_download/" + id,
			"filename":  filename,
			"expiresAt": expiresAt,
		}, nil
	})

	reg.RegisterRaw("/workspace_download/", func(w http.ResponseWriter, req *http.Request) {
		id := strings.TrimPrefix(req.URL.Path, "/workspace_download/")
		id = strings.TrimSuffix(id, "/")
		ticket, ok := store.consume(id)
		if !ok {
			web.WritePlain(w, http.StatusNotFound, "Download ticket is invalid or expired")
			return
		}
		if ticket.kind == "file" {
			target, err := resolveDownloadFileTarget(ticket.rootPath, ticket.relativePath)
			if err != nil {
				web.WritePlain(w, http.StatusNotFound, "File does not exist")
				return
			}
			f, info, oerr := openForDownload(target)
			if oerr != nil {
				web.WritePlain(w, http.StatusInternalServerError, "Failed to open file")
				return
			}
			defer f.Close()
			streamFileToWriter(w, f, info.Size(), ticket.filename)
			return
		}
		dirPath, dirName, err := resolveDownloadDirTarget(ticket.rootPath, ticket.relativePath)
		if err != nil {
			web.WritePlain(w, http.StatusNotFound, "Directory does not exist")
			return
		}
		_ = dirName
		streamZipToWriter(w, dirPath, strings.TrimSuffix(ticket.filename, ".zip"))
	})
}
