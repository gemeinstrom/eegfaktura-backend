# VFEEG Backend

The VFEEG (Verein zur Förderung von erneuerbaren Energiegemeinschaften) Backend is a Go-based service designed for energy management, participant tracking, and metering point operations. It provides a robust API (GraphQL and REST) to interact with energy data, integrates with MQTT for real-time messaging, and uses PostgreSQL for data persistence.

Part of the **eegfaktura** suite — an open-source billing and management platform
for Austrian renewable energy communities (*Erneuerbare-Energiegemeinschaften*, EEG).
It is the core domain service: it owns EEG, participant and metering-point data, and
consumes EDA market messages relayed from `eegfaktura-eda-xp` over MQTT.

**Tech stack:** Go · Gorilla mux (REST) · gqlgen (GraphQL) · gRPC · goqu ·
golang-migrate + Atlas · Eclipse Paho (MQTT) · Keycloak/OIDC (JWT) · Viper · logrus.
**Exposed ports:** HTTP (default `9080`, `8080` in container) and gRPC (`9092`).
**Talks to:** PostgreSQL, an MQTT broker (mosquitto), Keycloak, and `eegfaktura-eda-xp`.

## Features

- **GraphQL API**: Flexible data querying and manipulation using `gqlgen`.
- **REST API**: Standard endpoints for EEG, participants, metering, and processes.
- **MQTT Integration**: Real-time message handling and error subscriptions.
- **gRPC Support**: Internal service-to-service communication.
- **Database Migrations**: Automated schema management using `golang-migrate` and `atlas`.
- **Dockerized**: Easy deployment using Docker and multi-stage builds.
- **Keycloak Integration**: Secure authentication and authorization.

## Prerequisites

- **Go**: version 1.26 or higher.
- **PostgreSQL**: for data storage.
- **MQTT Broker**: (e.g., Mosquitto) for real-time messaging.
- **Protoc**: Protocol Buffers compiler (the build generates the gRPC code; CI pins 29.3).

## Getting Started

### Installation

1. Clone the repository.
2. Download dependencies:
   ```bash
   go mod download
   ```
3. Generate the gRPC code and the registration model (not tracked; protoc must be on the `PATH`, the
   generators are installed at the versions in `go.mod`):
   ```bash
   make tools generate
   ```

### Configuration

The application uses `config.yaml` for configuration. You can find a template in the root directory.

Key configuration options:
- `port`: HTTP server port (default: 9080).
- `database`: Connection details for PostgreSQL.
- `mqtt`: Connection details for the MQTT broker.
- `grpc-provider`: Port for the gRPC server.

The Keycloak clients are read from the JSON file named in `KEYCLOAK_CONFIG` (default `./keycloak.json`). The
`keycloak.json` in the repository is a sample: provide your own file with the secret of the
`at.ourproject.vfeeg.api` client and never commit it.

### Building

You can build the project using the provided `Makefile`:

```bash
make build
```

Or manually:

```bash
go build -o vfeeg-backend server.go
```

### Running

To run the application:

```bash
./vfeeg-backend -configPath .
```

Alternatively, use the `run` target in the `Makefile`:

```bash
make run
```

### Testing

Run the test suite using:

```bash
make test
```

The tests of `api`, `database` and `eda` start PostgreSQL through Testcontainers (Docker needed), one container per
package. On a shared or slow Docker (e.g. Docker Desktop) run them one package at a time:

```bash
go test -p 1 ./api ./database ./eda
```

## Development

### GraphQL

The project uses `gqlgen`. If you change the GraphQL schema (`graph/*.graphqls`), regenerate the code:

```bash
go generate ./...
```

### Protobuf/gRPC

The Go code of `proto/*.proto` and `model/registration.schema.json` is generated, not tracked. After changing one
of them, regenerate it:

```bash
make generate
```

### Database Migrations

Migrations are stored in the `migrations/` directory. The application automatically applies migrations on startup.

For schema diffing and hashing:
```bash
make atlas-hash
make atlas-diff
```

#### Failed migration (dirty)

The backend migrates before it serves (one replica, `Recreate`): while a migration runs or waits, the API is down.
Every lock wait of the migration connection ends after 60 s (`lock_timeout`; golang-migrate's own wait for its
migration lock ends after 15 s); the backend then exits with `event.action=db.migrate.failed` and restarts.

golang-migrate marks a version dirty in `base.schema_migrations` before it runs the file and clears the mark after
it. `dirty = true` therefore means: the file was started and did not finish (it failed, waited too long for a lock,
or the pod was stopped). The backend does not start on a dirty version. The migrate CLI is not in the image; recover
by hand with `psql`:

1. `SELECT version, dirty FROM base.schema_migrations;` and read the file of that version in `migrations/`.
2. Find out how much of the file is in place (cases below). Never only reset the version without this step.
3. File fully in place: `UPDATE base.schema_migrations SET version = <that version>, dirty = false;`.
   Not in place: undo what is left of it, then `UPDATE base.schema_migrations SET version = <the version before it>,
   dirty = false;` — the next start runs the file again.
4. Restart the backend and check the line `event.action=db.migrate.done`.

Cases (a new migration of a later change adds its row here, in the same change; for a version without a row,
follow the steps above):

| Dirty version | Check | Recovery |
|---|---|---|
| `20261105110000` (view `activeMeteringPartition`) | the statement either ran or not | `version = 20261028130000`: the file runs again (`CREATE OR REPLACE VIEW`) |
| `20261105110100` (index, `CONCURRENTLY`) | `SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass('base.metering_partition_factor_tenant_idx');` | no row or `false` (build failed or cancelled): `DROP INDEX CONCURRENTLY IF EXISTS base.metering_partition_factor_tenant_idx;`, then `version = 20261105110000` (with `IF NOT EXISTS` an invalid index would stay). `true` (the pod was stopped while the build waited; PostgreSQL finished the build): `version = 20261105110100` |
| `20261105120000` (table `settlement_interval_change`) | `SELECT to_regclass('base.settlement_interval_change');` | `version = 20261105110100`: the file runs again (`IF NOT EXISTS`) |
| `20261106090000` (view `billing_masterdata`, column `eeg_area`) | the statement either ran or not | `version = 20261105120000`: the file runs again (`CREATE OR REPLACE VIEW`) |
| `20261106100000` (columns `eeg.ec_zone_level`, `meteringpoint.tech_code`, `fuel_code`) | the file runs as one transaction: either all three columns are there or none (`SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = 'base' AND column_name IN ('ec_zone_level', 'tech_code', 'fuel_code');`) | `version = 20261106090000`: the file runs again (`IF NOT EXISTS`; the `UPDATE` fills only an empty `ec_zone_level`) |

### Rollback to an older image

An older image does not start on a database migrated by a newer one: the start-up migration exits with
`no migration found for version <newest version>: read down for version … file does not exist`, and the pod loops.
So the migrations go down **before** the older image starts. The image has no migrate CLI; run it from a checkout of
the **newer** release (it holds the down files), with `search_path=base` (the version is in `base.schema_migrations`):

```bash
migrate -path ./migrations \
  -database "postgres://<user>:<password>@<host>:5432/<db>?sslmode=<mode>&search_path=base" down <n>
```

| Back to | Newest version afterwards | `<n>` |
|---|---|---|
| before P2P/EVA (backend#147: `20261106090000`, `20261106100000`) | `20261105120000` | `2` |
| also before backend#145/#146 (`20261105110000`, `20261105110100`, `20261105120000`) | `20261028130000` | `5` |

`migrate … version` shows the current version first; `goto <version>` with the version of the second column does the
same as `down <n>`. Without the CLI, apply the `.down.sql` files newest first with `psql -f` (not in one transaction:
`DROP INDEX CONCURRENTLY` refuses that), then `UPDATE base.schema_migrations SET version = <version>, dirty = false`.

Order for the P2P/EVA release:

1. Revert the operator SQL (P2P communities back to their former type).
2. Roll back eegfaktura-billing#103 (it reads `eeg_area` of `base.billing_masterdata`).
3. Migrate down as above, while the new backend is scaled to 0 (it would migrate up again on a restart).
4. Start the older backend image.

A down step that fails halfway leaves `dirty = true` in `base.schema_migrations`: check what the failed file already did,
set the version with `migrate … force <version>` (the version before the failed step if it did nothing, else the
failed one), then repeat `down`.

The down migrations drop data: `ec_zone_level`, `tech_code`, `fuel_code` (after rolling forward again, with the next
list) and, behind backend#145, the records of `base.settlement_interval_change`.

## Docker

Build the Docker image:

```bash
docker build -t vfeeg-backend .
```

Run with Docker:

```bash
docker run -p 9080:8080 -v ./config.yaml:/etc/backend/config.yaml vfeeg-backend
```

## License

The eegfaktura application suite is open source under the GNU Affero General Public
License v3.0 (AGPL-3.0). See the [eegfaktura organisation](https://github.com/eegfaktura)
for the licensing applicable to this component.
