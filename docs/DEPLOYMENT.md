# Deployment

awxreport is a single static binary — drop it on any host that can reach the AWX controller. No Go runtime, no Python, no virtualenv.

## Air-gapped install

Useful when the target server has no internet but a jump host does.

1. **On a host with internet**: download the release archive matching the target's OS + architecture from the [Releases page](https://github.com/TheGU/awxreport/releases).

   ```bash
   # RHEL 9, x86_64
   curl -LO https://github.com/TheGU/awxreport/releases/download/v0.1.0/awxreport_0.1.0_linux_amd64.tar.gz
   curl -LO https://github.com/TheGU/awxreport/releases/download/v0.1.0/SHA256SUMS

   # Verify
   sha256sum -c SHA256SUMS --ignore-missing
   ```

2. **scp to the target**:

   ```bash
   scp awxreport_0.1.0_linux_amd64.tar.gz user@target:/tmp/
   ```

3. **On the target**:

   ```bash
   cd /opt
   sudo mkdir -p awxreport && cd awxreport
   sudo tar -xzf /tmp/awxreport_0.1.0_linux_amd64.tar.gz
   sudo cp config.example.yaml config.yaml
   sudo $EDITOR config.yaml         # set base_url at minimum
   ```

4. **Create the AWX token** (see [CONFIGURATION.md](CONFIGURATION.md#token-creation)).

5. **Validate connectivity**:

   ```bash
   export AWX_TOKEN=<token>
   ./awxreport probe
   ```

6. **Generate the report**:

   ```bash
   ./awxreport report
   ```

## Scheduled monthly run

A cron entry on the same host:

```cron
# Run on the first of every month at 02:00 local time, log everything.
0 2 1 * * cd /opt/awxreport && AWX_TOKEN=$(cat /etc/awxreport/token) ./awxreport report >> /var/log/awxreport.log 2>&1
```

Tips:

- Store the token in a file readable only by the cron user: `chmod 600 /etc/awxreport/token`.
- Log rotation: a `logrotate` snippet for `/var/log/awxreport.log` keeps disk usage bounded if you also enable `debug_dir`.
- The run is idempotent; re-running on the same window produces a fresh timestamped XLSX/CSV — old files are not touched.

## Cross-compile from source

You don't need pre-built binaries; any host with Go can produce them:

```bash
git clone https://github.com/TheGU/awxreport.git
cd awxreport

# Linux amd64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" -o bin/awxreport-linux-amd64 ./cmd/awxreport

# Linux arm64
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" -o bin/awxreport-linux-arm64 ./cmd/awxreport

# Windows amd64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" -o bin/awxreport-windows-amd64.exe ./cmd/awxreport
```

The binaries are static (no libc dependency) and around 11 MB each.

## Network requirements

awxreport is a pure HTTPS client.

- Outbound to the AWX controller on its configured port (typically 443).
- No inbound ports.
- No DNS lookups beyond what your OS does for the controller hostname.

If a corporate proxy sits between awxreport and AWX, the `HTTPS_PROXY` and `NO_PROXY` env vars are respected via Go's standard library defaults.

## Sizing

For an environment with 10k hosts and 100k completed jobs in a 30-day window:

- **CPU**: dominated by JSON parsing; one core is enough.
- **Memory**: peak ~250 MB during aggregation (sparse pair counters).
- **Network**: roughly `1 + jobs/page_size + jobs` requests — about 50k requests for the example above. At the default 200 ms pacing, that's roughly 3 hours.
- **Disk**: the XLSX is small (single-digit MB even with 500k pair rows); the detail CSV is the larger artefact, ~100 MB per million summaries.
