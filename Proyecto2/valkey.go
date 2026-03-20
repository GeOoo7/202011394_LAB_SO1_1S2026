// =========================================================
// storage/valkey.go — Cliente Valkey para métricas Grafana
// Proyecto 2 SO1 | Carnet: 202011394
// =========================================================

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"proyecto2/internal/parser"
)

// Claves en Valkey
const (
	KeyRAMHistory    = "so1:ram:history"     // Lista de puntos RAM en el tiempo
	KeyKilledHistory = "so1:killed:history"  // Lista de contenedores eliminados
	KeyTopRAM        = "so1:top:ram"         // Top 5 por RAM (JSON)
	KeyTopCPU        = "so1:top:cpu"         // Top 5 por CPU (JSON)
	KeyLatest        = "so1:snapshot:latest" // Último snapshot completo
	MaxHistory       = 2000                  // Máximo de entradas históricas
)

// ValkeyClient encapsula la conexión.
type ValkeyClient struct {
	rdb *redis.Client
	ctx context.Context
}

// NewValkeyClient crea el cliente y verifica la conexión con PING.
func NewValkeyClient() (*ValkeyClient, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         "localhost:6379",
		Password:     "",
		DB:           0,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping Valkey: %w", err)
	}
	return &ValkeyClient{rdb: rdb, ctx: ctx}, nil
}

// Close cierra la conexión.
func (v *ValkeyClient) Close() error { return v.rdb.Close() }

// ---- tipos para serialización ----

type ramPoint struct {
	TS         int64  `json:"ts"`
	TotalRAMKB uint64 `json:"total_ram_kb"`
	UsedRAMKB  uint64 `json:"used_ram_kb"`
	FreeRAMKB  uint64 `json:"free_ram_kb"`
}

type killedPoint struct {
	TS    int64 `json:"ts"`
	Count int   `json:"count"`
}

// StoreSnapshot persiste todas las métricas del ciclo actual en Valkey.
func (v *ValkeyClient) StoreSnapshot(snap *parser.SystemSnapshot, killed int) error {
	ts := snap.Timestamp.Unix()
	pipe := v.rdb.Pipeline()

	// 1. Historial de RAM (time-series list)
	ramJSON, _ := json.Marshal(ramPoint{
		TS: ts, TotalRAMKB: snap.TotalRAMKB,
		UsedRAMKB: snap.UsedRAMKB, FreeRAMKB: snap.FreeRAMKB,
	})
	pipe.LPush(v.ctx, KeyRAMHistory, ramJSON)
	pipe.LTrim(v.ctx, KeyRAMHistory, 0, MaxHistory-1)

	// 2. Historial de contenedores eliminados
	kJSON, _ := json.Marshal(killedPoint{TS: ts, Count: killed})
	pipe.LPush(v.ctx, KeyKilledHistory, kJSON)
	pipe.LTrim(v.ctx, KeyKilledHistory, 0, MaxHistory-1)

	// 3. Top 5 RAM y CPU (sobrescribe siempre)
	t5ram, _ := json.Marshal(parser.Top5ByRAM(snap.Processes))
	t5cpu, _ := json.Marshal(parser.Top5ByCPU(snap.Processes))
	pipe.Set(v.ctx, KeyTopRAM, t5ram, 24*time.Hour)
	pipe.Set(v.ctx, KeyTopCPU, t5cpu, 24*time.Hour)

	// 4. Snapshot completo (TTL 1h)
	full, _ := json.Marshal(snap)
	pipe.Set(v.ctx, KeyLatest, full, time.Hour)

	_, err := pipe.Exec(v.ctx)
	return err
}
