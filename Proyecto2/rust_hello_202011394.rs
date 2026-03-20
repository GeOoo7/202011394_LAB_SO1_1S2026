// =========================================================
// rust_hello_202011394.rs — Módulo Kernel en Rust (Extra)
// Carnet : 202011394  |  Marvin Geobani Pretzantzín Rosalío
// SO1 1S2026 — FIUSAC — USAC
//
// Actividad extra: módulo que imprime "Hola Mundo 202011394"
// en el log del kernel al cargarse y descargarse.
//
// COMPILACIÓN (requiere soporte Rust en el kernel ≥ 6.1):
//   make LLVM=1 -C /lib/modules/$(uname -r)/build \
//        M=$(pwd) modules
// =========================================================

#![no_std]
#![feature(allocator_api)]

use kernel::prelude::*;

module! {
    type: HelloModule,
    name: "rust_hello_202011394",
    author: "Marvin Geobani Pretzantzin Rosali - 202011394",
    description: "Modulo Rust — Actividad Extra SO1 2026",
    license: "GPL",
}

struct HelloModule;

impl kernel::Module for HelloModule {
    fn init(_module: &'static ThisModule) -> Result<Self> {
        pr_info!("Hola Mundo 202011394\n");
        Ok(HelloModule)
    }
}

impl Drop for HelloModule {
    fn drop(&mut self) {
        pr_info!("Hola Mundo 202011394 — modulo descargado\n");
    }
}
