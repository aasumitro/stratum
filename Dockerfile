# Multi-stage build for the two Go backend binaries.
#
# One `build` stage compiles both; two thin runtime stages (`api`, `worker`)
# each carry a single static binary on a distroless base. Select one with
#   podman build --target api    -t stratum-api    .
#   podman build --target worker -t stratum-worker .
#
# The runtime base has no shell, no package manager and no OS time-zone data.
# That last point is fine only because cmd/api and cmd/worker blank-import
# "time/tzdata", embedding the zoneinfo database in the binary — organization
# settings validation calls time.LoadLocation and would otherwise reject every
# IANA name on this base.

ARG GO_VERSION=1.27
ARG SERVICE_VERSION=dev

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

# Cache module downloads across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static, reproducible builds. CGO is off so the result runs on distroless/static.
#
# The build is tuned to fit a small builder: `-p 1` serialises package compiles,
# and GOMEMLIMIT/GOGC force the compiler to collect aggressively rather than let
# its heap grow. Without this a 2 GiB Podman machine OOM-kills the compile of the
# heavier packages (redis/go-redis, ugorji/codec). The two binaries build in
# separate steps so the first frees memory before the second; shared packages
# are already cached by then, so the second step is fast.
ENV CGO_ENABLED=0 GOFLAGS=-p=1 GOMEMLIMIT=1400MiB GOGC=25
RUN go build -trimpath -ldflags="-s -w" -o /out/stratum-api ./cmd/api
RUN go build -trimpath -ldflags="-s -w" -o /out/stratum-worker ./cmd/worker

# --- api -------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS api
ARG SERVICE_VERSION
ENV SERVICE_VERSION=${SERVICE_VERSION}
COPY --from=build /out/stratum-api /stratum-api
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/stratum-api"]

# --- worker --------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS worker
ARG SERVICE_VERSION
ENV SERVICE_VERSION=${SERVICE_VERSION}
COPY --from=build /out/stratum-worker /stratum-worker
USER nonroot
ENTRYPOINT ["/stratum-worker"]
