FROM node:22-alpine AS web
WORKDIR /src/frontend
RUN corepack enable
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
COPY api/ /src/api/
RUN pnpm build

FROM golang:1.25-alpine AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/ ./
COPY --from=web /src/frontend/dist ./cmd/nichosi/static
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nichosi ./cmd/nichosi

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/nichosi /nichosi
ENV NICHOSI_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/nichosi"]
