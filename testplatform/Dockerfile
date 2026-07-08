# Build from the repository root so the framework source is in context:
#   docker build -f testplatform/Dockerfile -t trpc-agent-testplatform .
FROM golang:1.24 AS build
WORKDIR /src
COPY . .
WORKDIR /src/testplatform
ENV CGO_ENABLED=0
RUN go build -o /out/testplatform .

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/testplatform /testplatform
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/testplatform", "-addr", ":8080", "-data", "/data"]
