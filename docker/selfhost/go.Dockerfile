FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go/go.mod go/go.sum ./
COPY go/cmd ./cmd
COPY go/internal ./internal
RUN CGO_ENABLED=0 go build -p 2 -o /gateway ./cmd/gateway && CGO_ENABLED=0 go build -p 2 -o /probe ./cmd/probe
RUN mkdir /notices && find /go/pkg/mod -type f \( -iname 'license*' -o -iname 'notice*' -o -iname 'copying*' \) -exec cp --parents {} /notices/ \;
FROM scratch
COPY --from=build /gateway /gateway
COPY --from=build /probe /probe
COPY --from=build /notices /licenses/modules
COPY --from=build /usr/local/go/LICENSE /licenses/Go-LICENSE.txt
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
USER 65532:65532
ENTRYPOINT ["/gateway"]
