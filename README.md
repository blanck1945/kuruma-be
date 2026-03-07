# Flota - API de multas (Go)

Servicio único en Go para consulta de multas en Argentina con:
- API externa e interna en el mismo backend.
- Respuestas estandarizadas (`success`, `data`, `error`, `meta`, `trace_id`, `timestamp`).
- Priorización de proveedores API con fallback a scraping.
- Base de performance (cache L1/L2, stale-while-revalidate, deduplicación de requests).
- Scraper real de CABA para consultar por patente o DNI.

## Estructura

- `cmd/server`: arranque del servidor
- `internal/api`: handlers HTTP + middlewares
- `internal/core`: casos de uso y validaciones
- `internal/providers`: router de proveedores + normalización canónica
- `internal/storage`: cache, métricas y logger
- `docs/openapi.yaml`: especificación OpenAPI
- `tests/load/k6.js`: prueba de carga

## Configuración

Copiar `.env.example` y definir variables.

Variables clave:
- `EXTERNAL_API_KEYS`: `cliente:apikey,cliente2:apikey2`
- `INTERNAL_JWT_SECRET`: secreto para validar JWT HS256 interno
- `REDIS_ADDR`: dirección Redis para cache L2
- `PBA_RECAPTCHA_TOKEN`: token de reCAPTCHA para consulta de infracciones en PBA
- `VEHICLE_PROFILE_API_URL`: URL base del proveedor para perfil vehicular por patente
- `VEHICLE_PROFILE_API_KEY`: API key del proveedor de perfil vehicular (si aplica)

## Autenticación

- API externa (`/v1/external/*`): header `X-API-Key`.
- API interna (`/v1/internal/*`): `Authorization: Bearer <jwt_hs256>` con `scope` que incluya `fines:read`.

Claims esperados para interno:
- `sub`: identificador de servicio cliente.
- `scope`: string con scopes (ej: `fines:read`).

## Endpoints

- `GET /v1/external/fines?plate=AAA000`
- `GET /v1/internal/fines?plate=AAA000`
- `GET /v1/external/fines?document=30111222`
- `GET /v1/external/vehicle-profile?plate=AAA000`
- `GET /v1/internal/vehicle-profile?plate=AAA000`
- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `GET /openapi.yaml`
- `GET /swagger`

## Ejecución

```bash
go run ./cmd/server
```

## Tests

```bash
go test ./...
```

Test live del scraper CABA por patente:

```bash
RUN_LIVE_CABA_TEST=1 go test ./internal/providers -run TestCABAScraperProvider_LiveByPlate -v
```

## Carga con k6

```bash
k6 run tests/load/k6.js -e BASE_URL=http://localhost:8080 -e API_KEY=external-secret-1
```

