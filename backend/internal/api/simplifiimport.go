package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Importing a Simplifi export from the app: the same Prepare and Write the
// `agentifi import` command runs, into the space the request is in, which must
// be empty.
//
// Two steps. The upload maps the file and answers with what it holds and what
// would stop it, writing nothing; the mapped rows wait in memory until the
// start, which writes them in the background because a large export takes
// longer than a request should. The client polls the job.
//
// Only the space's owner or an admin may import, and the assistant never can:
// the resource is in dispatchDeniedRoutes.

func init() {
	Register(Resource{Prefix: "/simplifi-import", Routes: func(rt *Routes) {
		rt.Write(http.MethodPost, "/", previewSimplifiImport)
		rt.Read(http.MethodGet, "/{id}", getSimplifiImport)
		rt.Write(http.MethodPost, "/{id}/start", startSimplifiImport)
	}})
}

const (
	// maxSimplifiExportBytes caps the export; a household with years of
	// history exports tens of megabytes, and the whole file is decoded in
	// memory.
	maxSimplifiExportBytes = 128 << 20
	// maxTransactionRulesBytes caps the separately saved rules response.
	maxTransactionRulesBytes = 16 << 20

	// A mapped export holds the whole ledger in memory, so an upload nobody
	// starts is dropped rather than kept.
	simplifiImportReadyFor = 30 * time.Minute
	// simplifiImportWriteLimit bounds the background write, which runs on no
	// request's context.
	simplifiImportWriteLimit = 15 * time.Minute
)

// Job states, as the client reads them.
const (
	simplifiImportReady   = "ready"
	simplifiImportRunning = "running"
	simplifiImportDone    = "done"
	simplifiImportFailed  = "failed"
)

// SimplifiImportPreview is what an upload holds and whether it can be written
// into this space.
type SimplifiImportPreview struct {
	// ID names the waiting job; empty when there is nothing to start.
	ID string `json:"id"`
	// Datasets is filled only when the export holds several and none was
	// chosen: the client asks, and uploads again with `dataset`.
	Datasets []string `json:"datasets"`
	// SpaceName is what this space is called once the import lands.
	SpaceName string `json:"space_name"`
	// Counts are the rows per table the write would produce.
	Counts map[string]int `json:"counts"`
	Rows   int            `json:"rows"`

	Errors []string `json:"errors"`
	// Warnings are grouped by kind, notes of what was not read last.
	Warnings []importer.Group `json:"warnings"`
	// Refusal is why this space cannot take the import, even though the file
	// is fine: it already holds data, or the export was imported before.
	Refusal string `json:"refusal"`
	// Summary is the report the command line prints.
	Summary   string `json:"summary"`
	CanImport bool   `json:"can_import"`
}

// SimplifiImportStatus is one job's progress. There is no percentage: the
// write is one transaction, and it is either running or it has finished.
type SimplifiImportStatus struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	SpaceName  string     `json:"space_name"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Rows       int        `json:"rows"`
	Error      string     `json:"error"`
}

type simplifiImportJob struct {
	id      uuid.UUID
	space   store.SpaceID
	status  SimplifiImportStatus
	mapped  *importer.Mapped
	expires time.Time
}

// simplifiImportJobs holds at most one job per space, on Env rather than
// package-level so two environments in one test binary do not share one.
type simplifiImportJobs struct {
	mu   sync.Mutex
	jobs map[store.SpaceID]*simplifiImportJob
}

// current is the space's job, if it has one that has not expired. The caller
// holds mu.
func (j *simplifiImportJobs) current(space store.SpaceID, now time.Time) *simplifiImportJob {
	job := j.jobs[space]
	if job != nil && job.status.State == simplifiImportReady && now.After(job.expires) {
		delete(j.jobs, space)
		return nil
	}
	return job
}

// find is the space's job by id, as a copy of its status.
func (j *simplifiImportJobs) find(space store.SpaceID, id uuid.UUID) (SimplifiImportStatus, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job := j.current(space, time.Now())
	if job == nil || job.id != id {
		return SimplifiImportStatus{}, false
	}
	return job.status, true
}

func previewSimplifiImport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if err := sp.RequireOwner(); err != nil {
		return err
	}
	if err := parseUpload(w, r, "file", maxSimplifiExportBytes+maxTransactionRulesBytes); err != nil {
		return err
	}
	export, err := requireUpload(r, "file", maxSimplifiExportBytes)
	if err != nil {
		return err
	}
	rules, _, err := readUpload(r, "transaction_rules", maxTransactionRulesBytes)
	if err != nil {
		return err
	}

	// Refused before mapping as well as at the start, so a person whose
	// import is already writing is told so rather than shown a preview.
	env.simplifiImports.mu.Lock()
	running := env.simplifiImports.current(sp.ID(), time.Now())
	env.simplifiImports.mu.Unlock()
	if running != nil && running.status.State == simplifiImportRunning {
		return errConflict("An import into this space is already running")
	}

	mapped, err := importer.Prepare(export.Bytes, rules.Bytes, strings.TrimSpace(r.FormValue("dataset")),
		importer.Options{OwnerEmail: sp.User.Email, IntoSpace: sp.ID()})
	var several *importer.SeveralDatasetsError
	if errors.As(err, &several) {
		return writeJSON(w, http.StatusOK, SimplifiImportPreview{
			Datasets: several.IDs,
			Refusal: fmt.Sprintf("This export holds %d Simplifi datasets. Choose the one to import.",
				len(several.IDs)),
		})
	}
	if err != nil {
		return errBadRequest("%s is not a Simplifi export this app can read: %s",
			export.Filename, strings.TrimPrefix(err.Error(), "importer: "))
	}

	preview := SimplifiImportPreview{
		SpaceName: mapped.Space.Name,
		Counts:    mapped.Report.Written,
		Rows:      mapped.RowCount(),
		Errors:    problems(mapped.Report.Errors),
		Warnings:  mapped.Report.Groups(),
		Summary:   mapped.Report.Render(),
	}
	if refusal := importer.Refusal(r.Context(), env.DB, mapped); refusal != nil {
		if !errors.Is(refusal, importer.ErrDuplicate) {
			return refusal
		}
		preview.Refusal = strings.TrimPrefix(refusal.Error(), importer.ErrDuplicate.Error()+": ")
	}
	preview.CanImport = mapped.Report.OK() && preview.Refusal == ""
	if !preview.CanImport {
		return writeJSON(w, http.StatusOK, preview)
	}

	job := &simplifiImportJob{
		id: uuid.New(), space: sp.ID(), mapped: mapped,
		expires: time.Now().Add(simplifiImportReadyFor),
	}
	job.status = SimplifiImportStatus{
		ID: job.id.String(), State: simplifiImportReady, SpaceName: mapped.Space.Name,
		Rows: mapped.RowCount(),
	}
	jobs := &env.simplifiImports
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if held := jobs.current(sp.ID(), time.Now()); held != nil && held.status.State == simplifiImportRunning {
		return errConflict("An import into this space is already running")
	}
	if jobs.jobs == nil {
		jobs.jobs = map[store.SpaceID]*simplifiImportJob{}
	}
	jobs.jobs[sp.ID()] = job
	preview.ID = job.status.ID
	return writeJSON(w, http.StatusOK, preview)
}

func getSimplifiImport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if err := sp.RequireOwner(); err != nil {
		return err
	}
	id, err := pathUUID(r, "id", "Import")
	if err != nil {
		return err
	}
	status, ok := env.simplifiImports.find(sp.ID(), id)
	if !ok {
		return errNotFound("Import")
	}
	return writeJSON(w, http.StatusOK, status)
}

func startSimplifiImport(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if err := sp.RequireOwner(); err != nil {
		return err
	}
	id, err := pathUUID(r, "id", "Import")
	if err != nil {
		return err
	}
	jobs := &env.simplifiImports
	jobs.mu.Lock()
	job := jobs.current(sp.ID(), time.Now())
	if job == nil || job.id != id {
		jobs.mu.Unlock()
		return errNotFound("Import")
	}
	if job.status.State != simplifiImportReady {
		jobs.mu.Unlock()
		return errConflict("This import has already been started")
	}
	started := time.Now().UTC()
	job.status.State, job.status.StartedAt = simplifiImportRunning, &started
	mapped := job.mapped
	status := job.status
	jobs.mu.Unlock()

	go env.writeSimplifiImport(job, mapped)
	return writeJSON(w, http.StatusAccepted, status)
}

// writeSimplifiImport runs the write on its own context: the request that
// started it returns at once, and a closed tab must not roll the import back.
func (env *Env) writeSimplifiImport(job *simplifiImportJob, mapped *importer.Mapped) {
	ctx, cancel := context.WithTimeout(context.Background(), simplifiImportWriteLimit)
	defer cancel()
	err := importer.Write(ctx, env.DB, mapped)

	jobs := &env.simplifiImports
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	finished := time.Now().UTC()
	job.status.FinishedAt = &finished
	job.mapped = nil
	if err != nil {
		slog.Error("simplifi import failed", "space", job.space, "error", err)
		job.status.State = simplifiImportFailed
		job.status.Error = strings.TrimPrefix(
			strings.TrimPrefix(err.Error(), importer.ErrDuplicate.Error()+": "), "importer: ")
		return
	}
	job.status.State = simplifiImportDone
}
