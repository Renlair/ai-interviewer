FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /app .

FROM gcr.io/distroless/static
COPY --from=build /app /app
EXPOSE 8080
ENTRYPOINT ["/app"]
