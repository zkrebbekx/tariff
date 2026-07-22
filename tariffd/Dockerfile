# syntax=docker/dockerfile:1
# tariffd — multi-stage build to a static binary in a distroless image.
#
# Build from the REPOSITORY ROOT, not this directory: the tariffd module
# depends on the tariff root module via a replace directive.
#
#   docker build -f tariffd/Dockerfile -t tariffd .
#
# The final image is distroless/static: no shell, no libc, no package manager
# — the binary is the whole attack surface, and CGO_ENABLED=0 (over a
# zero-dependency, pure-Go library) is what makes that possible.

FROM golang:1.25-alpine AS build
WORKDIR /src

# Module files first, so the (empty — tariff is zero-dependency) module graph
# resolves before the source is copied.
COPY go.mod ./
COPY tariffd/go.mod ./tariffd/
RUN cd tariffd && go mod download

COPY . .
RUN cd tariffd && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
    -o /tariffd ./cmd/tariffd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tariffd /tariffd
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/tariffd"]
