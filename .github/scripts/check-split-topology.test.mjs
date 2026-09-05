import assert from "node:assert/strict";
import test from "node:test";

import { validateSplitTopology } from "./check-split-topology.mjs";

function hardened(target) {
  return {
    build: { target },
    read_only: true,
    cap_drop: ["ALL"],
    security_opt: ["no-new-privileges:true"],
    tmpfs: ["/tmp:rw,noexec,nosuid,nodev,size=32m"],
    pids_limit: 64
  };
}

function validConfiguration() {
  return {
    services: {
      gateway: {
        ...hardened("gateway"),
        ports: [{ host_ip: "127.0.0.1", target: 8080, published: "7080" }],
        networks: { split: { ipv4_address: "172.31.237.254" } },
        depends_on: {
          backend: { condition: "service_healthy" },
          frontend: { condition: "service_healthy" }
        }
      },
      frontend: { ...hardened("frontend"), expose: ["8080"], networks: { split: null } },
      backend: {
        ...hardened("backend"),
        expose: ["8080"],
        networks: { split: null },
        environment: {
          XBOARD_FRONTEND_ORIGIN: "http://frontend:8080",
          XBOARD_WEB_ROOT: "",
          XBOARD_PANEL_URL: "http://127.0.0.1:7080",
          XBOARD_ALLOWED_ORIGINS: "http://127.0.0.1:7080",
          XBOARD_TRUSTED_PROXY_CIDRS: "172.31.237.254/32"
        },
        secrets: [
          { source: "bootstrap_admin_password" },
          { source: "settings_encryption_key" }
        ],
        volumes: [{ type: "volume", source: "data", target: "/var/lib/xboard" }]
      },
      mailpit: { ports: [{ host_ip: "127.0.0.1", target: 8025, published: "7082" }] }
    },
    networks: { split: { ipam: { config: [{ subnet: "172.31.237.0/24" }] } } }
  };
}

test("accepts a same-origin, least-privilege split topology", () => {
  assert.equal(validateSplitTopology(validConfiguration()), true);
});

test("rejects public application ports and backend or frontend publication", () => {
  const publicGateway = validConfiguration();
  publicGateway.services.gateway.ports[0].host_ip = "0.0.0.0";
  assert.throws(() => validateSplitTopology(publicGateway), /loopback/);

  const publicBackend = validConfiguration();
  publicBackend.services.backend.ports = [{ host_ip: "127.0.0.1", target: 8080 }];
  assert.throws(() => validateSplitTopology(publicBackend), /backend must not publish/);
});

test("rejects proxy trust broader than the exact gateway address", () => {
  const configuration = validConfiguration();
  configuration.services.backend.environment.XBOARD_TRUSTED_PROXY_CIDRS = "172.31.237.0/24";
  assert.throws(() => validateSplitTopology(configuration), /trust only the exact gateway/);
});

test("rejects secrets, volumes, or application settings on static tiers", () => {
  for (const mutation of [
    (configuration) => { configuration.services.gateway.secrets = [{ source: "settings_encryption_key" }]; },
    (configuration) => { configuration.services.frontend.volumes = [{ type: "volume", target: "/data" }]; },
    (configuration) => { configuration.services.frontend.environment = { TOKEN: "unsafe" }; }
  ]) {
    const configuration = validConfiguration();
    mutation(configuration);
    assert.throws(() => validateSplitTopology(configuration), /must not/);
  }
});

test("rejects a mutable root or missing process hardening", () => {
  for (const [field, value] of [
    ["read_only", false],
    ["cap_drop", []],
    ["security_opt", []],
    ["tmpfs", []],
    ["pids_limit", undefined]
  ]) {
    const configuration = validConfiguration();
    configuration.services.frontend[field] = value;
    assert.throws(() => validateSplitTopology(configuration));
  }
});

test("rejects an address outside the configured network", () => {
  const configuration = validConfiguration();
  configuration.networks.split.ipam.config[0].subnet = "172.31.238.0/24";
  assert.throws(() => validateSplitTopology(configuration), /belong to the configured private subnet/);
});
