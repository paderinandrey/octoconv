# OctoConv API docs

`openapi.yaml` is an OpenAPI 3.1 spec describing the OctoConv HTTP API
exactly as implemented in `internal/api/`.

## Import into Yaak

Yaak -> **Import** -> **OpenAPI** -> select `docs/openapi.yaml`.

## Auth

Every `/v1/*` route requires an API key: `Authorization: ApiKey <key>`
(the literal "ApiKey " prefix, then the raw key). `GET /healthz` is public.

## Base URL

The spec defines a `base_url` server variable, defaulting to
`http://localhost:8090` (the local docker-compose deployment's `API_ADDR`).
Override it in Yaak's environment settings to point at another deployment.
