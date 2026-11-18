# AGENTS.md — contrato operativo

> Subordinado a [`constitution.md`](constitution.md). Las reglas viven en
> [`gates.yaml`](gates.yaml); acá solo se citan. Si algo de este archivo
> contradice el registro, gana el registro.

## 1. Orden de autoridad

`constitution.md` → `gates.yaml` → `specs/<f>/spec.md` → `plan.md` → `tasks.md`.

## 2. Roles y perfiles

El rol entra por entorno, nunca por prompt: `PI_ROLE`.

| `PI_ROLE` | Qué hace | Qué no hace |
| :--- | :--- | :--- |
| `dispatch` | compila specs del registro, despacha, rutea wakes | editar, verificar |
| `implement` | edita dentro de `may_edit`, verifica, reporta | tocar gobernanza |
| `review` | juzga el cambio en contexto limpio | arreglar, mergear su worktree |
| `solo` (default) | uso interactivo con humano | — |

Detalle por rol: [`perfiles-sesion.md`](perfiles-sesion.md).
Protocolo de orquestación: [`protocolo-multiagente.md`](protocolo-multiagente.md).

## 3. No negociables

1. **El tier se computa del diff.** El agente puede escalarlo, nunca bajarlo.
2. **No se cierra sin verificación.** `verify-report.json` debe atar el árbol que
   se verifica (`worktree_tree_sha`).
3. **No se edita gobernanza.** `governance.paths` del registro; violarlo es
   `block` del `tool_call`, no un comentario en el review.
4. **Nada se satisface por ausencia.** Si no podés mostrar el inventario, el gate
   no pasó.
5. **Ningún comentario re-declara una regla.** Se cita `§X.Y` y el gate cita de
   vuelta.

## 4. Comandos

```bash
go run ./tools/harness/cmd/harness-verify -tier 0 -registry gates.yaml
go run ./tools/harness/cmd/harness-facts  -root . -measure
go run ./tools/harness/cmd/registry-gen   -registry gates.yaml -facts facts.json -write
go run ./tools/harness/cmd/harness-validate -kind spec|report|verdict <file>
go run ./tools/harness/cmd/nolint-budget  -root . -budget $GATE_INVENTORY/nolint-budget.json
```

## 5. Definición de terminado

1. `harness-verify` verde para el tier computado (o `not_applicable(bootstrap)`).
2. El reporte nombra el `tree_sha` verificado, no un commit lejano.
3. Si el cambio debilita un gate, no hay merge sin `Override-Gate-Reason` y
   aprobación de `CODEOWNERS`.
