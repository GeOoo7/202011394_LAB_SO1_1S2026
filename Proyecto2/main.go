// =========================================================
// main.go — Daemon Gestor de Contenedores
// Proyecto 2 SO1 1S2026
// Carnet: 202011394 | Marvin Geobani Pretzantzín Rosalío
// =========================================================

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"proyecto2/internal/manager"
	"proyecto2/internal/parser"
	"proyecto2/internal/storage"
)

const (
	PROC_FILE     = "/proc/continfo_pr2_so1_202011394"
	LOOP_INTERVAL = 20 * time.Second
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("[Daemon-202011394] ==========================================")
	log.Println("[Daemon-202011394] Iniciando servicio SO1 Proyecto 2...")
	log.Println("[Daemon-202011394] ==========================================")

	// 1. Levantar Grafana + Valkey via Docker Compose
	log.Println("[Daemon] Paso 1: Iniciando infraestructura (Grafana + Valkey)...")
	if err := manager.StartInfrastructure(); err != nil {
		log.Fatalf("[Daemon] FATAL: no se pudo iniciar infraestructura: %v", err)
	}

	// 2. Conectar a Valkey
	log.Println("[Daemon] Paso 2: Conectando a Valkey...")
	db, err := storage.NewValkeyClient()
	if err != nil {
		log.Fatalf("[Daemon] FATAL: no se pudo conectar a Valkey: %v", err)
	}
	defer db.Close()
	log.Println("[Daemon] Conexión a Valkey OK.")

	// 3. Cargar módulo de kernel
	log.Println("[Daemon] Paso 3: Cargando módulo de kernel...")
	if err := manager.LoadKernelModule(); err != nil {
		log.Printf("[Daemon] ADVERTENCIA módulo kernel: %v", err)
	}

	// 4. Registrar cronjob
	log.Println("[Daemon] Paso 4: Registrando cronjob de contenedores...")
	if err := manager.RegisterCronJob(); err != nil {
		log.Fatalf("[Daemon] FATAL: no se pudo registrar cronjob: %v", err)
	}

	// Captura de señales para shutdown limpio
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("[Daemon] Señal recibida: %s — iniciando shutdown...", sig)
		cancel()
	}()

	// 5. Loop principal
	log.Printf("[Daemon] Paso 5: Loop principal activo (intervalo %v).", LOOP_INTERVAL)
	ticker := time.NewTicker(LOOP_INTERVAL)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Daemon] Removiendo cronjob antes de salir...")
			_ = manager.RemoveCronJob()
			log.Println("[Daemon] Servicio finalizado correctamente. Bye!")
			return

		case t := <-ticker.C:
			log.Printf("[Daemon] ── Iteración %s ──", t.Format("15:04:05"))

			// a) Leer /proc
			raw, err := parser.ReadProcFile(PROC_FILE)
			if err != nil {
				log.Printf("[Daemon] Error leyendo /proc: %v", err)
				continue
			}

			// b) Deserializar
			snap, err := parser.ParseSnapshot(raw)
			if err != nil {
				log.Printf("[Daemon] Error parseando snapshot: %v", err)
				continue
			}
			log.Printf("[Daemon] Snapshot: %d procesos | RAM usada: %d KB",
				len(snap.Processes), snap.UsedRAMKB)

			// c) Gestión de contenedores
			killed, err := manager.ManageContainers(snap)
			if err != nil {
				log.Printf("[Daemon] Error en gestión: %v", err)
			} else {
				log.Printf("[Daemon] Contenedores eliminados: %d", killed)
			}

			// d) Persistir en Valkey
			if err := db.StoreSnapshot(snap, killed); err != nil {
				log.Printf("[Daemon] Error guardando en Valkey: %v", err)
			}
		}
	}
}
