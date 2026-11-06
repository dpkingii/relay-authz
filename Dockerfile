FROM golang:1.27.3 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
COPY rules.yaml /rules.yaml
ENV RULES=/rules.yaml
EXPOSE 8080
ENTRYPOINT ["/server"]
