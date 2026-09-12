package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type HealthHandler struct {
	DB *pgxpool.Pool
}

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// Check responde 200 si la API y la conexión a Postgres están sanas,
// y 503 si Postgres no responde. Pensado para healthchecks de Docker
// y para que el frontend pueda mostrar "sin conexión" si aplica.
func (h *HealthHandler) Check(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	resp := healthResponse{Status: "ok", Database: "ok"}
	statusCode := http.StatusOK

	if err := h.DB.Ping(ctx); err != nil {
		resp.Status = "degraded"
		resp.Database = "unreachable"
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(resp)
}
