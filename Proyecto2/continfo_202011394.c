// =========================================================
// continfo_202011394.c  —  Módulo de Kernel SO1 1S2026
// Carnet : 202011394  |  Marvin Geobani Pretzantzín Rosalío
// FIUSAC — Universidad San Carlos de Guatemala
// =========================================================

#include <linux/module.h>
#include <linux/kernel.h>
#include <linux/init.h>
#include <linux/proc_fs.h>
#include <linux/seq_file.h>
#include <linux/sched.h>
#include <linux/sched/signal.h>
#include <linux/sched/mm.h>
#include <linux/mm.h>
#include <linux/mm_types.h>
#include <linux/slab.h>
#include <linux/uaccess.h>
#include <linux/string.h>
#include <linux/nsproxy.h>
#include <linux/pid_namespace.h>

#define PROC_NAME   "continfo_pr2_so1_202011394"
#define CMDLINE_MAX 512

MODULE_LICENSE("GPL");
MODULE_AUTHOR("Marvin Geobani Pretzantzin Rosali - 202011394");
MODULE_DESCRIPTION("Sonda de kernel para telemetria de contenedores SO1 2026");
MODULE_VERSION("2.0");

static struct proc_dir_entry *proc_entry;

/*
 * read_cmdline_from_mm — lee arg_start..arg_end del proceso
 * usando copy_from_user() para obtener la cmdline real.
 */
static bool read_cmdline_from_mm(struct mm_struct *mm, char *buf, size_t len)
{
    unsigned long start, end, sz;

    if (!mm) return false;

    spin_lock(&mm->arg_lock);
    start = mm->arg_start;
    end   = mm->arg_end;
    spin_unlock(&mm->arg_lock);

    if (!start || end <= start) return false;

    sz = min((unsigned long)(len - 1), end - start);
    if (copy_from_user(buf, (const char __user *)start, sz))
        return false;

    buf[sz] = '\0';
    /* args separados por NUL → espacios para JSON legible */
    {
        size_t i;
        for (i = 0; i < sz - 1; i++)
            if (buf[i] == '\0') buf[i] = ' ';
    }
    return true;
}

/*
 * show_proc_info — callback seq_file
 * Emite JSON con RAM global + array de procesos.
 */
static int show_proc_info(struct seq_file *m, void *v)
{
    struct task_struct *task;
    unsigned long total_pages, total_kb, free_kb, used_kb;
    bool first = true;

    total_pages = totalram_pages();
    total_kb    = total_pages * (PAGE_SIZE / 1024);
    free_kb     = global_zone_page_state(NR_FREE_PAGES) * (PAGE_SIZE / 1024);
    used_kb     = total_kb - free_kb;

    seq_printf(m,
        "{\n"
        "  \"total_ram_kb\": %lu,\n"
        "  \"free_ram_kb\":  %lu,\n"
        "  \"used_ram_kb\":  %lu,\n"
        "  \"processes\": [\n",
        total_kb, free_kb, used_kb);

    rcu_read_lock();
    for_each_process(task) {
        struct mm_struct  *mm;
        long               rss, vsz;
        unsigned long long cpu;
        unsigned long      mem_pct;
        char               cmdline[CMDLINE_MAX];

        if (!task->mm) continue;

        mm = get_task_mm(task);
        if (!mm) continue;

        vsz     = mm->total_vm;
        rss     = get_mm_rss(mm);
        mem_pct = (total_pages > 0) ? (unsigned long)(rss * 10000UL / total_pages) : 0;
        cpu     = task->utime + task->stime;

        memset(cmdline, 0, CMDLINE_MAX);
        if (!read_cmdline_from_mm(mm, cmdline, CMDLINE_MAX))
            strncpy(cmdline, task->comm, CMDLINE_MAX - 1);

        mmput(mm);

        /* Escapar comillas para JSON válido */
        {
            size_t i;
            for (i = 0; i < strlen(cmdline); i++)
                if (cmdline[i] == '"') cmdline[i] = '\'';
        }

        if (!first) seq_printf(m, ",\n");
        first = false;

        seq_printf(m,
            "    {\n"
            "      \"pid\":         %d,\n"
            "      \"name\":        \"%s\",\n"
            "      \"cmdline\":     \"%s\",\n"
            "      \"vsz_kb\":      %lu,\n"
            "      \"rss_kb\":      %lu,\n"
            "      \"mem_percent\": %lu,\n"
            "      \"cpu_percent\": %llu\n"
            "    }",
            task->pid,
            task->comm,
            cmdline,
            (unsigned long)(vsz * (PAGE_SIZE / 1024)),
            (unsigned long)(rss * (PAGE_SIZE / 1024)),
            mem_pct,
            cpu);
    }
    rcu_read_unlock();

    seq_printf(m, "\n  ]\n}\n");
    return 0;
}

static int proc_open(struct inode *inode, struct file *file)
{
    return single_open(file, show_proc_info, NULL);
}

static const struct proc_ops proc_fops = {
    .proc_open    = proc_open,
    .proc_read    = seq_read,
    .proc_lseek   = seq_lseek,
    .proc_release = single_release,
};

static int __init continfo_init(void)
{
    proc_entry = proc_create(PROC_NAME, 0444, NULL, &proc_fops);
    if (!proc_entry) {
        pr_err("[continfo_202011394] ERROR creando /proc/%s\n", PROC_NAME);
        return -ENOMEM;
    }
    pr_info("[continfo_202011394] v2.0 cargado — /proc/%s listo\n", PROC_NAME);
    return 0;
}

static void __exit continfo_exit(void)
{
    proc_remove(proc_entry);
    pr_info("[continfo_202011394] descargado — /proc/%s eliminado\n", PROC_NAME);
}

module_init(continfo_init);
module_exit(continfo_exit);
