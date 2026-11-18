// Waffynx harness extension for Pi — DRAFT, NO EJECUTADO.
//
// Este archivo implementa los cuatro perfiles de `perfiles-sesion.md` sobre la API
// de extensiones de Pi. No se pudo ejecutar ni verificar desde el entorno donde se
// escribió, así que antes de confiar en él hay que confirmar tres cosas contra
// `docs/extensions.md` de la versión instalada de Pi:
//
//   1. La forma del export (factory que recibe `pi`) y si debe ser sync o async.
//   2. Que `agent_before_settle` acepte `{ continue: true }` para forzar un turno
//      de corrección, y cómo se inyecta el digest del error en ese turno.
//   3. Que `input` dispare con `source: "rpc"` (el despacho de Orca) y que
//      `{ action: "handled" }` corte antes de llamar al modelo.
//
// Lo que NO hace: no compila specs ni escribe el DAG. Eso es del coordinador
// (`protocolo-multiagente.md` §3 y §5); acá solo se aplican los límites.
//
// Dependencias: los binarios `tools/harness/cmd/*` aterrizados por el Día-0.

import { execFileSync } from "node:child_process";
import { readFileSync, mkdirSync, writeFileSync, renameSync } from "node:fs";
import { join, dirname } from "node:path";
import { createHash } from "node:crypto";

const ROLE = process.env.PI_ROLE ?? "solo";
const TASK = process.env.PI_TASK_ID ?? "";
const ROOT = process.env.PI_WORKSPACE_ROOT ?? process.cwd();

const GOVERNANCE = [
  "gates.yaml", "constitution.md", "AGENTS.md", "FEATURE_MAP.md",
  ".golangci.yml", "Makefile", "CODEOWNERS", ".github/workflows/",
  "tools/", "scripts/", "specs/HARNESS.md", "specs/templates/",
];

const MAX_TURNS = Number(process.env.PI_MAX_TURNS ?? 6);
const BREAKER = join(gitCommonDir(ROOT), "orca", "breaker", `${TASK}.json`);

function gitCommonDir(root) {
  try {
    return execFileSync("git", ["-C", root, "rev-parse", "--git-common-dir"], { encoding: "utf8" }).trim();
  } catch {
    return join(root, ".git");
  }
}

function harness(cmd, args) {
  return execFileSync("go", ["run", `./tools/harness/cmd/${cmd}`, ...args], {
    cwd: ROOT, encoding: "utf8", env: { ...process.env, GOTOOLCHAIN: process.env.GOTOOLCHAIN ?? "go1.22.12" },
  });
}

function readJson(path, fallback) {
  try { return JSON.parse(readFileSync(path, "utf8")); } catch { return fallback; }
}

// Escritura atómica: el breaker nunca debe quedar a medias, porque el
// coordinador decide con él si re-despacha.
function writeJson(path, value) {
  try {
    mkdirSync(dirname(path), { recursive: true });
    const tmp = `${path}.tmp`;
    writeFileSync(tmp, JSON.stringify(value, null, 2));
    renameSync(tmp, path);
  } catch {
    // Si no se puede escribir el breaker, el hook no debe romper el turno.
  }
}

function treeSha() {
  try {
    return execFileSync("git", ["-C", ROOT, "stash", "create"], { encoding: "utf8" }).trim() ||
      execFileSync("git", ["-C", ROOT, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
  } catch { return ""; }
}

function lastGreenTree() {
  const path = join(ROOT, ".artifacts", "verify-report.json");
  const rep = readJson(path, null);
  return rep && rep.green ? rep.tree_sha ?? null : null;
}

function inAllowlist(path, allowlist) {
  return allowlist.some((glob) => {
    const prefix = glob.split(/[*?[]/)[0];
    return path.startsWith(prefix);
  });
}

function spec() {
  return readJson(join(gitCommonDir(ROOT), "orca", "tasks", `${TASK}.node.json`), null);
}

export default function activate(pi) {
  let registry = null;
  let turn = 0;
  let attempts = readJson(BREAKER, { attempts: 0 }).attempts ?? 0;

  pi.on("session_start", async () => {
    // Congelado: si el agente edita gates.yaml por bash, esta sesión no hereda
    // reglas debilitadas.
    try { registry = readFileSync(join(ROOT, "gates.yaml"), "utf8"); } catch { registry = null; }
  });

  pi.on("input", async () => {
    if (ROLE === "solo") return;
    const state = readJson(BREAKER, null);
    if (state?.state === "needs-human-spec") {
      // 0 tokens: el modelo no llega a ver el prompt.
      return { action: "handled" };
    }
  });

  pi.on("tool_call", async (event) => {
    if (event.tool !== "write" && event.tool !== "edit") return;

    const path = String(event.input?.path ?? "");
    const rel = path.startsWith(ROOT) ? path.slice(ROOT.length + 1).replaceAll("\\", "/") : path;

    if (ROLE === "dispatch" || ROLE === "review") {
      return { block: true, reason: `profile ${ROLE} no edita archivos` };
    }

    if (GOVERNANCE.some((g) => rel === g || rel.startsWith(g))) {
      return { block: true, reason: `gobernanza: ${rel} requiere PI_GATE_EDIT_AUTH y merge por CODEOWNERS` };
    }

    const sp = spec();
    if (ROLE === "implement" && sp?.may_edit && !inAllowlist(rel, sp.may_edit)) {
      return { block: true, reason: `fuera de may_edit (${sp.may_edit.join(", ")}): ${rel}` };
    }
    return;
  });

  pi.on("agent_before_settle", async () => {
    if (ROLE === "dispatch" || ROLE === "review") return; // verificar acá rompe el despacho

    turn += 1;
    const tree = treeSha();
    if (tree && lastGreenTree() === tree) return; // caché por árbol, no por turno

    let output = "";
    let ok = false;
    try {
      output = harness("harness-verify", ["-registry", "gates.yaml", "-tier", "0", "-out", ".artifacts/verify-report.json"]);
      ok = true;
    } catch (err) {
      output = String(err?.stdout ?? "") + String(err?.stderr ?? "");
      ok = false;
    }

    if (ok) {
      attempts = 0;
      writeJson(BREAKER, { attempts: 0, state: "open", updated_at: new Date().toISOString(), tree_sha: tree });
      return;
    }

    attempts += 1;
    const exhausted = attempts >= 3 || turn >= MAX_TURNS;
    writeJson(BREAKER, {
      attempts,
      state: exhausted ? "needs-human-spec" : "retrying",
      updated_at: new Date().toISOString(),
      tree_sha: tree,
      last_error_sha256: createHash("sha256").update(output).digest("hex"),
    });

    if (exhausted) return; // el breaker queda abierto; el coordinador no re-despacha

    // Un turno más: el error ya viaja en el resultado de esta verificación, y
    // Pi hace una request de corrección.
    return { continue: true };
  });
}
