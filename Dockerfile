FROM golang:1.10 as builder

COPY . /go/src/github.com/JulienBalestra/audit-trace

RUN make -C /go/src/github.com/JulienBalestra/audit-trace re

FROM busybox:latest

COPY --from=builder /go/src/github.com/JulienBalestra/audit-trace/audit-trace /usr/local/bin/audit-trace
