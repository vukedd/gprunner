# Stage 1: Build

FROM golang:1.25.5-alpine AS build

WORKDIR /app

COPY go.mod go.sum* ./

RUN go mod download

COPY . .

RUN go build -o build.bin ./cmd

# Stage 1b: patched kraft
#
# Stock kraft cannot attach a second guest to a bridge while another is
# running (its network Update re-creates every live tap and the kernel
# refuses). Built from fork

FROM golang:1.26-alpine AS kraft

ARG KRAFTKIT_REPO=https://github.com/vukedd/kraftkit
ARG KRAFTKIT_REF=vukedd/fix-bridge-update

RUN apk add --no-cache git

RUN git clone --depth 1 --branch "${KRAFTKIT_REF}" "${KRAFTKIT_REPO}" /kraftkit \
    && cd /kraftkit \
    && CGO_ENABLED=0 go build -o /kraft ./cmd/kraft

# Stage 2: Run

FROM kraftkit.sh/base:latest AS run

COPY --from=kraft /kraft /usr/local/bin/kraft

WORKDIR /app

COPY cmd/test_data ./test_data

COPY --from=build /app/build.bin /usr/bin/runner

ENTRYPOINT ["/usr/bin/runner"]