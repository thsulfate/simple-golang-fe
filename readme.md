```markdown
Simple Go Frontend — minimal, tidy, Go-first

Recommended minimal structure (tidy and easy to deploy)
- /opt/simple-frontend/
  - simple-frontend            # built binary
  - templates/
    - index.html
  - static/
    - styles.css

Build
1. go build -o simple-frontend

Install to a VM
1. Create directory and copy files:
   sudo mkdir -p /opt/simple-frontend
   sudo cp simple-frontend /opt/simple-frontend/
   sudo cp -r templates static /opt/simple-frontend/
   sudo chown -R root:root /opt/simple-frontend
   sudo chmod 755 /opt/simple-frontend/simple-frontend

2. Copy the systemd unit:
   sudo cp frontend.service /etc/systemd/system/simple-frontend.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now simple-frontend.service

Notes
- The binary looks for templates/ and static/ under -assets (defaults to current dir).
- Set BACKEND_URL (or the BACKEND_URL env in the service) to your internal backend farm (e.g., https://backend.internal).
- The unit provided runs the binary as www-data; if you prefer another user, adjust the unit.

Deployment tips for a frontend farm
- Deploy this package to each frontend VM under /opt/simple-frontend and enable the systemd unit.
- Put an external load balancer or edge reverse proxy in front of the frontends; use /healthz to probe instance health.
- Keep BACKEND_URL pointing to an internal VIP, internal load balancer, or backend service discovery.

If you want a single-file binary (no external templates/static), I can show how to embed assets using go:embed — but since you wanted separate files for tidiness, this layout keeps HTML and CSS editable on the server.
```