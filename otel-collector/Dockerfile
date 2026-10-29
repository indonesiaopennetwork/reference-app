# Companion OTel Collector sidecar for onix-bap/onix-bpp — receives OTLP/gRPC
# from onix's otelsetup plugin on localhost:4317, filters to only
# network-relevant signals, and forwards them to netcol.staging.ion.id. See
# config.yaml for what's filtered and why.
FROM otel/opentelemetry-collector-contrib:0.159.0

COPY config.yaml /etc/otel/config.yaml

CMD ["--config=/etc/otel/config.yaml"]
