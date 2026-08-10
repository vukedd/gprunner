# Stage 1: Build

FROM golang:1.25.5-alpine AS build

WORKDIR /app

COPY go.mod go.sum* ./

RUN go mod download

COPY . .

RUN go build -o build.bin ./cmd

# Stage 2: Run

FROM kraftkit.sh/base:latest AS run

COPY --from=build /app/build.bin /usr/bin/runner

ENTRYPOINT ["/usr/bin/runner"]