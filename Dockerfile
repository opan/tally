# Build with the latest Go patch release: older 1.26.x have net/http fixes
# tally needs (govulncheck). Dependabot bumps this tag.
FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /tally .

# Distroless: just CA certificates (for the aio login) and a non-root user,
# no shell or package manager. The tz database is embedded in the binary.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /tally /tally
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/tally"]
