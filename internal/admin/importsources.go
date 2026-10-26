package admin

import (
	"net/http"
	"strings"
	"time"

	"bin/internal/storage"
)

// Saved imports: a remote server and what to import from it, kept with a
// name so it can be run again (bringing what is new), edited or deleted.
// The secret key is stored encrypted and never sent back.

type importSourceJSON struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Endpoint     string     `json:"endpoint"`
	AccessKey    string     `json:"accessKey"`
	SecretKeySet bool       `json:"secretKeySet"`
	Region       string     `json:"region"`
	Buckets      []string   `json:"buckets"`
	Settings     bool       `json:"settings"`
	IAM          bool       `json:"iam"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastRunAt    *time.Time `json:"lastRunAt"`
	// Jobs are this import's copies started since bin last restarted.
	Jobs []importJob `json:"jobs"`
}

func (h *Handler) toImportSourceJSON(s storage.ImportSource) importSourceJSON {
	jobs := []importJob{}
	for _, j := range h.imports.list() {
		if j.Source == s.ID {
			jobs = append(jobs, j)
		}
	}
	buckets := s.Buckets
	if buckets == nil {
		buckets = []string{}
	}
	return importSourceJSON{
		ID: s.ID, Name: s.Name, Endpoint: s.Endpoint, AccessKey: s.AccessKey, SecretKeySet: s.SecretEnc != "",
		Region: s.Region, Buckets: buckets, Settings: s.Settings, IAM: s.IAM, CreatedAt: s.Created,
		LastRunAt: optionalTime(s.LastRun), Jobs: jobs,
	}
}

type importSourceInput struct {
	Name      string   `json:"name"`
	Endpoint  string   `json:"endpoint"`
	AccessKey string   `json:"accessKey"`
	SecretKey string   `json:"secretKey"` // empty on update: keep the stored one
	Region    string   `json:"region"`
	Buckets   []string `json:"buckets"`
	Settings  bool     `json:"settings"`
	IAM       bool     `json:"iam"`
}

// readImportSource validates the body into src; old is the saved import
// being edited, nil when creating one.
func (h *Handler) readImportSource(w http.ResponseWriter, r *http.Request, old *storage.ImportSource) (storage.ImportSource, bool) {
	var in importSourceInput
	if !readJSON(w, r, &in) {
		return storage.ImportSource{}, false
	}
	fail := func(field, msg string) (storage.ImportSource, bool) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_import", "message": msg, "field": field})
		return storage.ImportSource{}, false
	}
	in.Name, in.Endpoint, in.AccessKey = strings.TrimSpace(in.Name), strings.TrimSpace(in.Endpoint), strings.TrimSpace(in.AccessKey)
	src := storage.ImportSource{
		Name: in.Name, Endpoint: in.Endpoint, AccessKey: in.AccessKey, Region: in.Region,
		Buckets: in.Buckets, Settings: in.Settings, IAM: in.IAM,
	}
	if old != nil {
		src.ID, src.SecretEnc = old.ID, old.SecretEnc
	}
	if in.SecretKey != "" {
		src.SecretEnc = h.opts.Keys.Box.Encrypt(in.SecretKey)
	}
	switch {
	case in.Name == "" || len(in.Name) > 100:
		return fail("name", "Give the import a name of up to 100 characters.")
	case in.AccessKey == "":
		return fail("accessKey", "The access key is required.")
	case src.SecretEnc == "":
		return fail("secretKey", "The secret key is required.")
	case len(in.Buckets) == 0 && !in.IAM:
		return fail("buckets", "Select at least one bucket, or users and policies.")
	}
	if _, err := parseRemoteEndpoint(in.Endpoint); err != nil {
		_, msg := remoteErrorMessage(err)
		return fail("endpoint", msg)
	}
	if literalHostBlocked(in.Endpoint) {
		_, msg := remoteErrorMessage(errBlockedAddress)
		return fail("endpoint", msg)
	}
	for _, b := range in.Buckets {
		if !storage.ValidBucketName(b) {
			return fail("buckets", "Not a valid bucket name: "+b)
		}
	}
	return src, true
}

func (h *Handler) listImportSources(w http.ResponseWriter, r *http.Request) {
	sources, err := h.opts.Store.ListImportSources(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]importSourceJSON, 0, len(sources))
	for _, s := range sources {
		out = append(out, h.toImportSourceJSON(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{"imports": out})
}

func (h *Handler) createImportSource(w http.ResponseWriter, r *http.Request) {
	src, ok := h.readImportSource(w, r, nil)
	if !ok {
		return
	}
	src.ID = "imp_" + randomToken()[:12]
	setAuditResource(r, src.Name)
	if err := h.opts.Store.CreateImportSource(r.Context(), src); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("saved import created", "import", src.ID, "name", src.Name, "endpoint", src.Endpoint)
	created, err := h.opts.Store.ImportSource(r.Context(), src.ID)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.toImportSourceJSON(created))
}

func (h *Handler) updateImportSource(w http.ResponseWriter, r *http.Request) {
	old, err := h.opts.Store.ImportSource(r.Context(), r.PathValue("id"))
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	src, ok := h.readImportSource(w, r, &old)
	if !ok {
		return
	}
	setAuditResource(r, src.Name)
	if err := h.opts.Store.UpdateImportSource(r.Context(), src); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("saved import updated", "import", src.ID, "name", src.Name)
	updated, err := h.opts.Store.ImportSource(r.Context(), src.ID)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.toImportSourceJSON(updated))
}

func (h *Handler) deleteImportSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	setAuditResource(r, id)
	if err := h.opts.Store.DeleteImportSource(r.Context(), id); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("saved import deleted", "import", id)
	w.WriteHeader(http.StatusNoContent)
}

// runImportSource runs a saved import: new buckets are created, the others
// get what is new or changed on the remote.
func (h *Handler) runImportSource(w http.ResponseWriter, r *http.Request) {
	src, err := h.opts.Store.ImportSource(r.Context(), r.PathValue("id"))
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	setAuditResource(r, src.Name)
	for _, j := range h.imports.list() {
		if j.Source == src.ID && (j.State == "queued" || j.State == "running") {
			writeError(w, http.StatusConflict, "import_running", "This import is already running.")
			return
		}
	}
	secret, err := h.opts.Keys.Box.Decrypt(src.SecretEnc)
	if err != nil {
		h.opts.Log.Error("import: decrypting the saved secret key", "import", src.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "secret_unreadable", "The saved secret key could not be read; edit the import and enter it again.")
		return
	}
	if err := h.opts.Store.MarkImportSourceRun(r.Context(), src.ID); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.startImport(w, r, importStartRequest{
		importRemote: importRemote{Endpoint: src.Endpoint, AccessKey: src.AccessKey, SecretKey: secret, Region: src.Region},
		Buckets:      src.Buckets, Settings: src.Settings, IAM: src.IAM,
	}, src.ID)
}
