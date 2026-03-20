// =========================================================
// manager/manager.go — Gestión de contenedores Docker
// Proyecto 2 SO1 | Carnet: 202011394
// =========================================================

package manager

import (
	"fmt"
	"log"
	"os/exec"
	"strings"

	"proyecto2/internal/parser"
)

const (
	MIN_LOW  = 3  // mínimo contenedores bajo consumo
	MIN_HIGH = 2  // mínimo contenedores alto consumo

	IMG_GO_CLIENT = "roldyoran/go-client"
	IMG_ALPINE    = "alpine"

	COMPOSE_FILE  = "./docker/docker-compose.yml"
	KERNEL_SCRIPT = "./scripts/load_kernel.sh"
	CRON_SCRIPT   = "./scripts/containers_cron.sh"
	CRON_SCHEDULE = "*/2 * * * *"
)

// StartInfrastructure levanta Grafana y Valkey con Docker Compose.
func StartInfrastructure() error {
	out, err := exec.Command("docker", "compose", "-f", COMPOSE_FILE, "up", "-d").CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose: %v\n%s", err, out)
	}
	log.Printf("[Manager] Infraestructura OK:\n%s", out)
	return nil
}

// LoadKernelModule ejecuta el script que compila e inserta el .ko.
func LoadKernelModule() error {
	out, err := exec.Command("bash", KERNEL_SCRIPT).CombinedOutput()
	if err != nil {
		return fmt.Errorf("load_kernel.sh: %v\n%s", err, out)
	}
	log.Printf("[Manager] Kernel module OK:\n%s", out)
	return nil
}

// RegisterCronJob agrega la entrada en crontab si no existe.
func RegisterCronJob() error {
	cur, _ := exec.Command("crontab", "-l").Output()
	if strings.Contains(string(cur), CRON_SCRIPT) {
		log.Println("[Manager] Cronjob ya registrado.")
		return nil
	}
	newCron := string(cur) + CRON_SCHEDULE + " bash " + CRON_SCRIPT + "\n"
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(newCron)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("crontab: %v", err)
	}
	log.Println("[Manager] Cronjob registrado.")
	return nil
}

// RemoveCronJob elimina la línea del crontab al finalizar.
func RemoveCronJob() error {
	cur, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(cur), "\n") {
		if !strings.Contains(l, CRON_SCRIPT) {
			lines = append(lines, l)
		}
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	return cmd.Run()
}

// ---- helpers Docker ----

func runningContainers() ([]string, error) {
	out, err := exec.Command("docker", "ps", "-q").Output()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func containerImage(id string) string {
	out, _ := exec.Command("docker", "inspect",
		"--format={{.Config.Image}}", id).Output()
	return strings.TrimSpace(string(out))
}

func containerName(id string) string {
	out, _ := exec.Command("docker", "inspect",
		"--format={{.Name}}", id).Output()
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "/")
}

func killContainer(id string) error {
	exec.Command("docker", "stop", id).Run()
	return exec.Command("docker", "rm", "-f", id).Run()
}

// isInfra detecta contenedores de infraestructura que NO deben eliminarse.
func isInfra(img, name string) bool {
	for _, kw := range []string{"grafana", "valkey", "redis"} {
		if strings.Contains(img, kw) || strings.Contains(name, kw) {
			return true
		}
	}
	return false
}

// isHighConsumption clasifica un contenedor como "alto consumo".
func isHighConsumption(img string) bool {
	return img == IMG_GO_CLIENT ||
		strings.Contains(img, "go-client") ||
		strings.Contains(img, "bc")        // alpine con carga CPU (creado por cron)
}

// ManageContainers analiza el snapshot y elimina contenedores sobrantes
// respetando: ≥3 bajo consumo, ≥2 alto consumo, nunca Grafana/Valkey.
// Retorna el número de contenedores eliminados.
func ManageContainers(snap *parser.SystemSnapshot) (int, error) {
	ids, err := runningContainers()
	if err != nil {
		return 0, err
	}

	var high, low []string
	for _, id := range ids {
		img  := containerImage(id)
		name := containerName(id)
		if isInfra(img, name) {
			continue
		}
		if isHighConsumption(img) {
			high = append(high, id)
		} else {
			low = append(low, id)
		}
	}

	log.Printf("[Manager] Inventario: %d alto consumo, %d bajo consumo",
		len(high), len(low))

	// Loggear top rankings del snapshot
	topRAM := parser.Top5ByRAM(snap.Processes)
	topCPU := parser.Top5ByCPU(snap.Processes)
	log.Printf("[Manager] Top5 RAM: %v", topRAM)
	log.Printf("[Manager] Top5 CPU: %v", topCPU)

	killed := 0

	// Eliminar excedentes de ALTO consumo (mantener MIN_HIGH)
	for len(high) > MIN_HIGH {
		id := high[len(high)-1]
		high = high[:len(high)-1]
		if err := killContainer(id); err != nil {
			log.Printf("[Manager] Error eliminando high %s: %v", id, err)
		} else {
			log.Printf("[Manager] Eliminado contenedor alto consumo: %s", id)
			killed++
		}
	}

	// Eliminar excedentes de BAJO consumo (mantener MIN_LOW)
	for len(low) > MIN_LOW {
		id := low[len(low)-1]
		low = low[:len(low)-1]
		if err := killContainer(id); err != nil {
			log.Printf("[Manager] Error eliminando low %s: %v", id, err)
		} else {
			log.Printf("[Manager] Eliminado contenedor bajo consumo: %s", id)
			killed++
		}
	}

	log.Printf("[Manager] Estado final: %d high, %d low | Eliminados: %d",
		len(high), len(low), killed)
	return killed, nil
}
