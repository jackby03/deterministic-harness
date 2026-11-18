#!/usr/bin/env bash
# Aserción de binario estático (Constitution §2.1).
#
# Extracción literal del job `static-build` de .github/workflows/ci.yml:81-95.
# No agrega lógica: mueve la comprobación a un gate para que también corra fuera
# de CI y para que su resultado quede en el reporte.
set -u

status=0
for b in waf-api waf-agent; do
    if [ ! -f "bin/$b" ]; then
        echo "FAIL bin/$b no existe: correr 'make build' primero"
        status=1
        continue
    fi
    out=$(ldd "bin/$b" 2>&1 || true)
    case "$out" in
        *"not a dynamic executable"*|*"statically linked"*)
            echo "OK  $b estáticamente linkeado"
            ;;
        *)
            echo "FAIL $b no es estático (Constitution §2.1): $out"
            status=1
            ;;
    esac
done
exit $status
