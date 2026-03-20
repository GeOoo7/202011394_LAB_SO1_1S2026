# Manual Técnico — Proyecto 2 SO1 1S2026
## Sonda de Kernel en C y Daemon en Go para la Telemetría de Contenedores

| Campo | Detalle |
|---|---|
| **Carnet** | 202011394 |
| **Estudiante** | Marvin Geobani Pretzantzín Rosalío |
| **Curso** | Sistemas Operativos 1 — 1S2026 |
| **Universidad** | FIUSAC — Universidad San Carlos de Guatemala |
| **Fecha** | Marzo 2026 |
| **Ponderación** | 15 puntos |

---

## Índice

1. [Introducción](#1-introducción)
2. [Arquitectura del Sistema](#2-arquitectura-del-sistema)
3. [Módulo de Kernel en C](#3-módulo-de-kernel-en-c)
4. [Actividad Extra — Módulo en Rust](#4-actividad-extra--módulo-de-kernel-en-rust)
5. [Daemon en Go](#5-daemon-en-go)
6. [Infraestructura Docker](#6-infraestructura-docker)
7. [Valkey — Persistencia de Métricas](#7-valkey--persistencia-de-métricas)
8. [Dashboard de Grafana](#8-dashboard-de-grafana)
9. [Guía de Instalación](#9-guía-de-instalación)
10. [Estructura del Repositorio](#10-estructura-del-repositorio)
11. [Decisiones de Diseño](#11-decisiones-de-diseño-y-justificaciones)
12. [Referencias](#12-referencias)

---

## 1. Introducción

Este documento es el manual técnico completo del Proyecto 2 de Sistemas Operativos 1 (1S2026). El proyecto implementa un sistema integral de telemetría y gestión autónoma de contenedores Docker compuesto por cuatro componentes principales que trabajan en conjunto.

| Componente | Rol en el sistema |
|---|---|
| **Módulo Kernel (C)** | Sonda de bajo nivel que accede directamente a `task_struct` del kernel Linux para capturar métricas de RAM y CPU, exponiéndolas en `/proc`. |
| **Daemon (Go)** | Cerebro del sistema: lee `/proc`, toma decisiones autónomas sobre contenedores, persiste métricas en Valkey y gestiona el ciclo de vida completo del servicio. |
| **Valkey + Grafana** | Base de datos de series de tiempo y dashboard de visualización en tiempo real. |
| **Módulo Rust (Extra)** | Módulo adicional Hello World en Rust, demostrando el soporte de lenguajes modernos en el kernel Linux ≥ 6.1. |

El proyecto combina programación de bajo nivel (módulos de kernel en C/Rust) con programación de alto nivel (daemon en Go), abordando un problema real en entornos contenerizados: la monitorización y estabilización autónoma del sistema.

---

## 2. Arquitectura del Sistema

### 2.1 Flujo de Datos

```
┌─────────────────────────────────────────────────────────┐
│               DAEMON GO (Orquestador Central)           │
│                                                         │
│  Paso 1 → docker compose up  →  Grafana + Valkey        │
│                                                         │
│  Paso 2 → bash load_kernel.sh  →  insmod .ko            │
│                                                         │
│  Paso 3 → crontab (*/2 min)  →  5 contenedores Docker   │
│                                                         │
│  Loop cada 20s:                                         │
│    /proc/continfo_pr2_so1_202011394  →  JSON métricas   │
│    Análisis  →  Detener/Eliminar contenedores           │
│    Valkey (series de tiempo)  ←→  Grafana (dashboard)   │
└─────────────────────────────────────────────────────────┘
```

### 2.2 Restricciones Clave del Sistema

El daemon garantiza en todo momento el cumplimiento de estas invariantes:

| Restricción | Implementación en el Daemon |
|---|---|
| ≥ 3 contenedores de bajo consumo | El manager clasifica `alpine sleep` y solo elimina los que superen el mínimo de 3. |
| ≥ 2 contenedores de alto consumo | Idem para `go-client` y `alpine` con carga CPU; se mantienen siempre 2. |
| Nunca eliminar Grafana ni Valkey | Se detectan por nombre de imagen (`grafana`, `valkey`, `redis`) y se excluyen de candidatos a eliminación. |
| Ordenamiento para decisiones | Los procesos se ordenan por RSS, VSZ, %mem y %cpu antes de calcular top 5 y decidir eliminaciones. |

---

## 3. Módulo de Kernel en C

### 3.1 Descripción General

El módulo `continfo_202011394` es un **Loadable Kernel Module (LKM)** para Linux que implementa una entrada en el sistema de archivos virtual `/proc`. Al ser leído, el archivo genera dinámicamente un documento JSON con métricas actualizadas del sistema sin necesidad de reiniciar el kernel.

> **Archivo expuesto:** `/proc/continfo_pr2_so1_202011394`

### 3.2 Campos JSON Generados

| Campo JSON | Fuente en el Kernel | Descripción |
|---|---|---|
| `total_ram_kb` | `totalram_pages() × PAGE_SIZE/1024` | RAM total del sistema en kilobytes. |
| `free_ram_kb` | `NR_FREE_PAGES × PAGE_SIZE/1024` | Páginas de RAM libres convertidas a KB. |
| `used_ram_kb` | `total_ram_kb − free_ram_kb` | RAM actualmente en uso. |
| `pid` | `task_struct→pid` | Identificador de proceso (PID). |
| `name` | `task_struct→comm` | Nombre corto del proceso (máx. 15 caracteres). |
| `cmdline` | `mm→arg_start..arg_end` vía `copy_from_user()` | Línea de comando completa real del proceso. |
| `vsz_kb` | `mm→total_vm × PAGE_SIZE/1024` | Tamaño total de memoria virtual en KB (VSZ). |
| `rss_kb` | `get_mm_rss(mm) × PAGE_SIZE/1024` | Memoria física residente en RAM en KB (RSS). |
| `mem_percent` | `(rss / total_pages) × 10000` | Porcentaje de memoria (×100: `1234` = `12.34%`). |
| `cpu_percent` | `task→utime + task→stime` | Tiempo acumulado de CPU en jiffies (valor diferencial). |

### 3.3 Estructuras Internas del Kernel Utilizadas

#### `task_struct`
Estructura central de cada proceso en Linux. El módulo la itera con `for_each_process()` dentro de una sección protegida por `rcu_read_lock()` / `rcu_read_unlock()` para acceso seguro a la lista enlazada de procesos sin bloquear el scheduler.

#### `mm_struct`
Descriptor del espacio de memoria virtual de un proceso. Se obtiene con `get_task_mm()` que incrementa el contador de referencias y debe liberarse con `mmput()`. Proporciona:
- `total_vm`: tamaño total de la memoria virtual (VSZ).
- `arg_start` / `arg_end`: punteros al buffer de argumentos en el espacio de usuario.
- `get_mm_rss(mm)`: suma las páginas anónimas, de archivo y de swap del proceso (RSS).

#### `seq_file API`
Interfaz del kernel para crear entradas `/proc` con contenido dinámico y potencialmente mayor que `PAGE_SIZE`. Se usa `single_open()` que gestiona automáticamente el buffer. Los datos se emiten con `seq_printf()`.

#### `proc_ops`
Estructura moderna (kernel ≥ 5.6) que registra los callbacks del archivo `/proc`: `proc_open`, `proc_read`, `proc_lseek` y `proc_release`.

### 3.4 Lectura de cmdline con `copy_from_user()`

La línea de comando real reside en el espacio de memoria virtual del proceso. Para leerla:

1. Se obtienen `mm→arg_start` y `mm→arg_end` bajo `spin_lock(&mm→arg_lock)`.
2. Se calcula el tamaño: `sz = min(CMDLINE_MAX − 1, arg_end − arg_start)`.
3. Se copia desde el espacio de usuario con `copy_from_user(buf, (const char __user *)arg_start, sz)`.
4. Los bytes `NUL` que separan argumentos se reemplazan por espacios para producir JSON legible.
5. Si la copia falla, se usa `task→comm` como fallback.

### 3.5 Formato de Salida JSON

```json
{
  "total_ram_kb": 16384000,
  "free_ram_kb":  4096000,
  "used_ram_kb":  12288000,
  "processes": [
    {
      "pid":         2847,
      "name":        "containerd-shim",
      "cmdline":     "containerd-shim-runc-v2 -namespace moby -id abc123",
      "vsz_kb":      712448,
      "rss_kb":      14336,
      "mem_percent": 87,
      "cpu_percent": 395820
    },
    {
      "pid":         2901,
      "name":        "sleep",
      "cmdline":     "sleep 240",
      "vsz_kb":      1536,
      "rss_kb":      512,
      "mem_percent": 3,
      "cpu_percent": 120
    }
  ]
}
```

### 3.6 Makefile y Targets de Compilación

| Target | Descripción |
|---|---|
| `make all` | Compila el módulo usando los headers del kernel activo (`/lib/modules/$(uname -r)/build`). |
| `make clean` | Elimina todos los artefactos de compilación (`.ko`, `.o`, `.mod.c`, etc.). |
| `make load` | Ejecuta `sudo insmod continfo_202011394.ko` para insertar el módulo. |
| `make unload` | Ejecuta `sudo rmmod continfo_202011394` para descargarlo limpiamente. |
| `make status` | Muestra si el módulo está cargado con `lsmod` y el contenido actual de `/proc`. |
| `make logs` | Filtra los últimos mensajes del módulo en el ring buffer del kernel (`dmesg`). |

---

## 4. Actividad Extra — Módulo de Kernel en Rust

Como actividad adicional y opcional se implementó un módulo de kernel en Rust (`rust_hello_202011394`) que imprime el mensaje de identificación en el log del kernel al cargarse y descargarse.

> **Requisito:** Kernel Linux ≥ 6.1 compilado con `CONFIG_RUST=y` y toolchain `rustup` con target `x86_64-unknown-none`.

### 4.1 Mensajes Generados

Al cargar (`sudo insmod rust_hello_202011394.ko`):
```
[  123.456789] rust_hello_202011394: Hola Mundo 202011394
```

Al descargar (`sudo rmmod rust_hello_202011394`):
```
[  456.789012] rust_hello_202011394: Hola Mundo 202011394 — modulo descargado
```

### 4.2 Diferencias con el Módulo en C

| Aspecto | Módulo en C | Módulo en Rust |
|---|---|---|
| **Seguridad de memoria** | Manual, propenso a null pointers y race conditions. | Garantizada por el borrow checker: previene use-after-free y data races. |
| **Compilación** | GCC con `make -C $(KDIR) M=$(PWD)` | LLVM/Clang con `LLVM=1` en el Makefile. |
| **Soporte kernel** | Todas las versiones de Linux. | Solo kernel ≥ 6.1 con `CONFIG_RUST=y`. |
| **Madurez** | Ecosistema maduro, API completa y estable. | En desarrollo activo; algunas APIs aún no disponibles en Rust. |

---

## 5. Daemon en Go

### 5.1 Descripción General

El daemon es el componente orquestador del sistema. Se ejecuta como proceso persistente en segundo plano y coordina todos los demás componentes. Implementado en Go 1.21+ con tres paquetes internos bajo `internal/` para garantizar encapsulamiento y bajo acoplamiento.

### 5.2 Paquetes Internos

| Paquete | Archivo | Responsabilidad |
|---|---|---|
| `cmd` | `cmd/main.go` | Punto de entrada. Ciclo de vida completo: inicio, loop y shutdown. |
| `internal/parser` | `parser.go` | Lee y deserializa el JSON de `/proc`. Ordenamiento (RAM, CPU, VSZ, RSS) y rankings Top5. |
| `internal/manager` | `manager.go` | Opera sobre Docker: infraestructura, carga kernel, cronjob, clasifica y elimina contenedores. |
| `internal/storage` | `valkey.go` | Cliente Valkey con pipeline. Almacena historial RAM, killed, top5 RAM y CPU. |

### 5.3 Ciclo de Vida Detallado

#### Fase 1 — Inicio del Servicio
- Levanta Grafana y Valkey: `docker compose -f ./docker/docker-compose.yml up -d`
- Establece conexión con Valkey en `localhost:6379` y verifica con `PING`.
- Si Valkey no responde, el daemon termina con error fatal.

#### Fase 2 — Carga del Módulo de Kernel
- Ejecuta `bash ./scripts/load_kernel.sh` que compila (`make`) e inserta el módulo (`insmod`).
- Si el módulo ya estaba cargado, `rmmod` lo descarga antes de reinsertarlo.
- Verifica que `/proc/continfo_pr2_so1_202011394` exista y sea legible.

#### Fase 3 — Registro del Cronjob
- Lee el crontab actual con `crontab -l`.
- Si la entrada no existe, la agrega: `*/2 * * * * bash ./scripts/containers_cron.sh`
- Si ya existe (reinicio del daemon), no la duplica.

#### Fase 4 — Loop Principal (cada 20 segundos)
1. Lee el contenido de `/proc/continfo_pr2_so1_202011394` con `os.ReadFile()`.
2. Deserializa el JSON en un `SystemSnapshot` con `encoding/json`.
3. Registra métricas clave en el log del sistema.
4. Invoca `ManageContainers(snap)` que clasifica contenedores y elimina excedentes.
5. Persiste el snapshot completo en Valkey usando un pipeline de comandos Redis.

#### Fase 5 — Shutdown Limpio
- Captura señales `SIGINT` y `SIGTERM` mediante `signal.Notify()` en una goroutine.
- Al recibir señal, cancela el contexto principal con `cancel()`.
- El loop detecta `ctx.Done()`, remueve el cronjob y termina limpiamente.

### 5.4 Lógica de Gestión de Contenedores

La función `ManageContainers()` del paquete `manager` implementa el siguiente algoritmo:

1. Ejecuta `docker ps -q` para obtener los IDs de contenedores activos.
2. Para cada ID, consulta `docker inspect --format={{.Config.Image}}` para obtener la imagen.
3. Clasifica el contenedor como:
   - **Infraestructura** (`grafana`, `valkey`) → nunca tocar.
   - **Alto consumo** (`go-client`, `alpine` con `bc`).
   - **Bajo consumo** (`alpine sleep`).
4. Si hay más de 2 contenedores de alto consumo, elimina los sobrantes.
5. Si hay más de 3 contenedores de bajo consumo, elimina los sobrantes.
6. Cada eliminación ejecuta `docker stop <id>` seguido de `docker rm -f <id>`.
7. Retorna el total de contenedores eliminados en esa iteración.

### 5.5 Dependencias del Proyecto Go

| Librería | Versión | Uso |
|---|---|---|
| `github.com/redis/go-redis/v9` | v9.5.1 | Cliente Redis/Valkey. Provee pipeline, PING, SET, LPUSH, LRANGE. |
| `encoding/json` | stdlib | Deserialización del JSON producido por el módulo de kernel. |
| `os/exec` | stdlib | Ejecución de comandos `docker`, `crontab`, `bash`. |
| `os/signal + syscall` | stdlib | Captura de SIGINT/SIGTERM para shutdown limpio. |
| `context` | stdlib | Cancelación del loop principal y propagación del shutdown. |

---

## 6. Infraestructura Docker

### 6.1 docker-compose.yml

El archivo define la infraestructura de soporte en una red privada `so1_network` para que Grafana pueda comunicarse con Valkey usando el nombre de servicio como hostname.

| Servicio | Imagen | Puerto | Persistencia |
|---|---|---|---|
| `valkey` | `valkey/valkey:latest` | 6379 | Volumen `valkey_data` montado en `/data` |
| `grafana` | `grafana/grafana:latest` | 3000 | Volumen `grafana_data` + provisioning en `/etc/grafana/provisioning` |

> **Acceso a Grafana:** `http://localhost:3000` — Usuario: `admin` — Contraseña: `admin`

### 6.2 Cronjob — Script `containers_cron.sh`

Registrado con la expresión `*/2 * * * *` (cada 2 minutos). Despliega 5 contenedores seleccionados aleatoriamente entre tres perfiles:

| Categoría | Imagen | Comando Docker |
|---|---|---|
| Alto RAM | `roldyoran/go-client` | `docker run -d roldyoran/go-client` |
| Alto CPU | `alpine` | `docker run -d alpine sh -c "while true; do echo '2^20' \| bc > /dev/null; sleep 2; done"` |
| Bajo consumo | `alpine` | `docker run -d alpine sleep 240` |

---

## 7. Valkey — Persistencia de Métricas

### 7.1 Modelo de Datos

Todas las métricas se almacenan con el prefijo `so1:`:

| Clave | Tipo Redis | Contenido |
|---|---|---|
| `so1:ram:history` | List | JSON: `{ts, total_ram_kb, used_ram_kb, free_ram_kb}`. Máximo 2000 entradas (LTRIM). |
| `so1:killed:history` | List | JSON: `{ts, count}`. Número de contenedores eliminados por iteración. |
| `so1:top:ram` | String | JSON: array Top 5 procesos por RSS. TTL: 24 horas. |
| `so1:top:cpu` | String | JSON: array Top 5 procesos por CPU. TTL: 24 horas. |
| `so1:snapshot:latest` | String | JSON: snapshot completo del último ciclo. TTL: 1 hora. |

### 7.2 Optimización con Pipeline

Todas las escrituras de un ciclo se agrupan en un pipeline de Redis (`pipe := v.rdb.Pipeline()`) y se ejecutan con una sola llamada de red (`pipe.Exec()`). Esto reduce la latencia de 5 round-trips a 1 round-trip por iteración.

---

## 8. Dashboard de Grafana

### 8.1 Provisioning Automático

El dashboard se configura automáticamente mediante archivos de provisioning al iniciar el contenedor. No requiere configuración manual en la UI.

| Archivo de provisioning | Propósito |
|---|---|
| `datasources/valkey.yml` | Registra Valkey como fuente de datos usando el plugin Redis Data Source. |
| `dashboards/provider.yml` | Indica a Grafana dónde buscar los archivos JSON de dashboards. |
| `dashboards/contenedores_202011394.json` | Definición completa del dashboard con todos los paneles. |

### 8.2 Paneles del Dashboard

El dashboard **"Panel de Contenedores - 202011394"** implementa el layout del documento de especificación:

| Panel | Tipo | Fuente de datos (Valkey) |
|---|---|---|
| Total RAM | Stat / Card | `JSON.GET so1:snapshot:latest $.total_ram_kb` |
| Free RAM | Stat / Card | `JSON.GET so1:snapshot:latest $.free_ram_kb` |
| Total Contenedores Eliminados | Stat / Card | `LLEN so1:killed:history` |
| Gráfica de Uso de RAM en el Tiempo | Time Series | `LRANGE so1:ram:history 0 99` |
| Top 5 Contenedores por RAM | Pie Chart | `GET so1:top:ram` |
| Top 5 Contenedores por CPU | Pie Chart | `GET so1:top:cpu` |
| RAM Usada | Stat / Card | `JSON.GET so1:snapshot:latest $.used_ram_kb` |

---

## 9. Guía de Instalación

### 9.1 Requisitos Previos

| Herramienta | Instalación |
|---|---|
| Kernel headers | `sudo apt install linux-headers-$(uname -r) build-essential` |
| Go 1.21+ | `wget https://go.dev/dl/go1.21.linux-amd64.tar.gz` → `tar -C /usr/local -xzf go1.21*` |
| Docker Engine | `sudo apt install docker.io docker-compose-plugin` → `sudo systemctl enable --now docker` |
| cron | `sudo apt install cron` → `sudo systemctl enable --now cron` |
| Permisos root | El daemon requiere privilegios de root para `insmod`/`rmmod` y manipular crontab. |

### 9.2 Pasos de Instalación

**Paso 1 — Clonar el repositorio**
```bash
git clone <URL_REPOSITORIO_PRIVADO>
cd <CARNET>_LAB_SO1_1S2026/proyecto2
```

**Paso 2 — Compilar el módulo de kernel**
```bash
cd kernel_module
make
ls -la continfo_202011394.ko   # Verificar que se generó
cd ..
```

**Paso 3 — Compilar el daemon de Go**
```bash
cd daemon_go
go mod download
go build -o ../daemon ./cmd/
cd ..
```

**Paso 4 — Dar permisos a los scripts**
```bash
chmod +x scripts/load_kernel.sh
chmod +x scripts/containers_cron.sh
```

**Paso 5 — Ejecutar el daemon**
```bash
sudo ./daemon
```

El daemon iniciará automáticamente Grafana, Valkey, el módulo de kernel y el cronjob.

### 9.3 Verificación del Sistema

| ¿Qué verificar? | Comando |
|---|---|
| Módulo cargado | `lsmod \| grep continfo_202011394` |
| Archivo /proc legible | `cat /proc/continfo_pr2_so1_202011394 \| python3 -m json.tool` |
| Logs del kernel | `sudo dmesg \| grep continfo_202011394` |
| Contenedores activos | `docker ps` |
| Grafana accesible | `curl -s http://localhost:3000/api/health` |
| Datos en Valkey | `redis-cli -p 6379 KEYS 'so1:*'` |
| Cronjob registrado | `crontab -l \| grep containers_cron.sh` |

---

## 10. Estructura del Repositorio

```
proyecto2/
├── kernel_module/
│   ├── continfo_202011394.c          # Módulo de kernel en C
│   └── Makefile                      # Build system (all, clean, load, unload, status, logs)
│
├── rust_module/
│   ├── rust_hello_202011394.rs       # Módulo extra en Rust (Hola Mundo 202011394)
│   ├── Kbuild                        # Declaración del objeto para el kernel
│   └── Makefile                      # Build system con LLVM=1
│
├── daemon_go/
│   ├── cmd/
│   │   └── main.go                   # Punto de entrada del daemon
│   ├── internal/
│   │   ├── parser/
│   │   │   └── parser.go             # Lectura /proc, deserialización JSON, top5
│   │   ├── manager/
│   │   │   └── manager.go            # Gestión de contenedores Docker y cronjob
│   │   └── storage/
│   │       └── valkey.go             # Cliente Valkey con pipeline
│   └── go.mod                        # Módulo Go y dependencias
│
├── scripts/
│   ├── load_kernel.sh                # Compila e inserta el módulo de kernel
│   └── containers_cron.sh           # Genera 5 contenedores aleatorios (invocado por crontab)
│
├── docker/
│   └── docker-compose.yml            # Servicios Grafana + Valkey en red so1_network
│
├── grafana/
│   └── provisioning/
│       ├── datasources/
│       │   └── valkey.yml            # Datasource Valkey para Grafana
│       └── dashboards/
│           ├── provider.yml          # Provider de dashboards
│           └── contenedores_202011394.json  # Dashboard completo con 7 paneles
│
├── docs/
│   └── manual_tecnico_202011394.md   # Este documento
│
└── README.md                         # Guía rápida de inicio
```

---

## 11. Decisiones de Diseño y Justificaciones

### 11.1 JSON como Formato de `/proc`
Se eligió JSON sobre texto plano estructurado por las siguientes razones:
- Deserialización directa en Go con `encoding/json` sin parsers personalizados.
- Extensible: agregar nuevos campos no rompe versiones anteriores del daemon.
- Legible por humanos para depuración manual con `python3 -m json.tool`.
- Estándar ampliamente adoptado que facilita integración con otras herramientas.

### 11.2 `seq_file` sobre `file_operations` Directas
La API `seq_file` gestiona automáticamente el buffering cuando la salida supera `PAGE_SIZE` (4096 bytes), lo que ocurre frecuentemente con decenas de procesos. Es la forma recomendada por la documentación oficial del kernel para entradas `/proc` con contenido dinámico.

### 11.3 `get_task_mm()` sobre Acceso Directo a `task→mm`
Se usa `get_task_mm()` en lugar de acceder directamente a `task→mm` para garantizar que el `struct mm_struct` no sea destruido entre el momento de obtenerlo y su uso. Esta función incrementa el contador de referencias y requiere `mmput()` al finalizar. El acceso directo sin incrementar la referencia causaría **use-after-free** en condiciones de carrera.

### 11.4 Pipeline de Redis en el Storage
Las 5 escrituras a Valkey por ciclo se agrupan en un pipeline que las envía en una sola llamada de red, reduciendo 5 round-trips a 1. Es una buena práctica que se mantiene incluso si se acorta el intervalo del loop.

### 11.5 `context.WithCancel()` para Shutdown Limpio
El uso de un contexto cancelable garantiza que el daemon siempre ejecute el cleanup (remover el cronjob) antes de terminar, independientemente de si la señal llega durante una lectura de `/proc`, durante una operación Docker o durante la espera del ticker. Esto previene que el cronjob continúe ejecutándose después de que el daemon se detenga, evitando acumulación indefinida de contenedores.

---

## 12. Referencias

- The Linux Kernel Documentation — https://www.kernel.org/doc/html/latest/
- Linux Kernel Module Programming Guide (sysprog21) — https://sysprog21.github.io/lkmpg/
- Linux kernel source: `include/linux/sched.h` (task_struct), `mm_types.h` (mm_struct)
- Rust for Linux — https://rust-for-linux.com/
- The Go Programming Language Documentation — https://go.dev/doc/
- go-redis/v9 Documentation — https://redis.uptrace.dev/
- Valkey Documentation — https://valkey.io/docs/
- Grafana Redis Data Source Plugin — https://grafana.com/grafana/plugins/redis-datasource/
- Docker Engine Documentation — https://docs.docker.com/engine/
- Documento de especificación: Proyecto 2 SO1 1S2026 — FIUSAC, USAC

---

*Carnet 202011394 | Marvin Geobani Pretzantzín Rosalío | SO1 1S2026 | FIUSAC — USAC*
