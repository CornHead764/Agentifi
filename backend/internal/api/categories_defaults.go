package api

import (
	"net/http"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The starter category tree.
//
// Seeding is idempotent: a category that already exists by name is counted as
// existing and left as the user has it, so running it on a space that already
// has categories adds only what is missing.

type CategoryDefaultsResponse struct {
	Created    int                `json:"created"`
	Existing   int                `json:"existing"`
	Categories []CategoryResponse `json:"categories"`
}

func seedDefaultCategories(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	result, err := env.DB.SeedCategories(r.Context(), sp.ID(), domain.DefaultCategories)
	if err != nil {
		return err
	}
	rows, err := env.DB.ListCategories(r.Context(), sp.ID(), false)
	if err != nil {
		return err
	}
	out := CategoryDefaultsResponse{
		Created:    result.Created,
		Existing:   result.Existing,
		Categories: make([]CategoryResponse, 0, len(rows)),
	}
	for _, row := range rows {
		out.Categories = append(out.Categories, categoryResponse(row))
	}
	status := http.StatusOK
	if result.Created > 0 {
		status = http.StatusCreated
	}
	return writeJSON(w, status, out)
}
