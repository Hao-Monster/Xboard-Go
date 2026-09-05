import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

function requireCondition(condition, message) {
  if (!condition) throw new Error(message);
}

function list(value) {
  return Array.isArray(value) ? value : [];
}

function ipv4ToInteger(value) {
  const octets = value.split(".");
  requireCondition(octets.length === 4, `invalid IPv4 address: ${value}`);
  let result = 0;
  for (const octet of octets) {
    requireCondition(/^\d{1,3}$/.test(octet), `invalid IPv4 address: ${value}`);
    const numeric = Number(octet);
    requireCondition(numeric <= 255, `invalid IPv4 address: ${value}`);
    result = (result * 256) + numeric;
  }
  return result;
}

function cidrContains(cidr, address) {
  const [network, prefixText, ...extra] = cidr.split("/");
  requireCondition(extra.length === 0 && /^\d{1,2}$/.test(prefixText ?? ""), `invalid IPv4 CIDR: ${cidr}`);
  const prefix = Number(prefixText);
  requireCondition(prefix >= 0 && prefix <= 32, `invalid IPv4 CIDR: ${cidr}`);
  const divisor = 2 ** (32 - prefix);
  return Math.floor(ipv4ToInteger(network) / divisor) === Math.floor(ipv4ToInteger(address) / divisor);
}

function assertHardened(name, service) {
  requireCondition(service.read_only === true, `${name} root filesystem must be read-only`);
  requireCondition(list(service.cap_drop).includes("ALL"), `${name} must drop every Linux capability`);
  requireCondition(
    list(service.security_opt).includes("no-new-privileges:true"),
    `${name} must enable no-new-privileges`
  );
  requireCondition(list(service.tmpfs).some((entry) => entry.startsWith("/tmp:")), `${name} must use a bounded /tmp tmpfs`);
  requireCondition(Number.isInteger(service.pids_limit) && service.pids_limit > 0, `${name} must have a PID limit`);
}

function assertNoApplicationSecrets(name, service) {
  requireCondition(list(service.secrets).length === 0, `${name} must not mount application secrets`);
  requireCondition(list(service.volumes).length === 0, `${name} must not mount application volumes`);
  requireCondition(Object.keys(service.environment ?? {}).length === 0, `${name} must not receive application environment values`);
}

export function validateSplitTopology(config) {
  requireCondition(config && typeof config === "object", "Compose configuration must be an object");
  const services = config.services ?? {};
  const gateway = services.gateway;
  const frontend = services.frontend;
  const backend = services.backend;
  requireCondition(gateway && frontend && backend, "gateway, frontend, and backend services are required");

  for (const [name, service, target] of [
    ["gateway", gateway, "gateway"],
    ["frontend", frontend, "frontend"],
    ["backend", backend, "backend"]
  ]) {
    requireCondition(service.build?.target === target, `${name} must build the ${target} image target`);
    assertHardened(name, service);
  }

  const gatewayPorts = list(gateway.ports);
  requireCondition(gatewayPorts.length === 1, "gateway must publish exactly one port");
  requireCondition(
    gatewayPorts[0].host_ip === "127.0.0.1" && Number(gatewayPorts[0].target) === 8080,
    "gateway application port must bind only to loopback"
  );
  requireCondition(list(frontend.ports).length === 0, "frontend must not publish a host port");
  requireCondition(list(backend.ports).length === 0, "backend must not publish a host port");
  requireCondition(list(frontend.expose).map(Number).includes(8080), "frontend must expose its private port");
  requireCondition(list(backend.expose).map(Number).includes(8080), "backend must expose its private port");

  assertNoApplicationSecrets("gateway", gateway);
  assertNoApplicationSecrets("frontend", frontend);

  const backendEnvironment = backend.environment ?? {};
  requireCondition(backendEnvironment.XBOARD_FRONTEND_ORIGIN === "http://frontend:8080", "backend must use the private frontend origin");
  requireCondition(backendEnvironment.XBOARD_WEB_ROOT === "", "backend must not serve an embedded frontend in split mode");
  requireCondition(
    backendEnvironment.XBOARD_PANEL_URL === backendEnvironment.XBOARD_ALLOWED_ORIGINS,
    "panel URL and browser origin must remain same-origin"
  );
  requireCondition(
    /^http:\/\/127\.0\.0\.1:\d+$/.test(backendEnvironment.XBOARD_ALLOWED_ORIGINS ?? ""),
    "split browser origin must be the loopback gateway"
  );
  requireCondition(
    list(backend.secrets).some((secret) => secret.source === "bootstrap_admin_password") &&
      list(backend.secrets).some((secret) => secret.source === "settings_encryption_key"),
    "backend must mount both file-backed application secrets"
  );
  requireCondition(
    list(backend.volumes).some((volume) => volume.type === "volume" && volume.target === "/var/lib/xboard"),
    "backend must own the application data volume"
  );

  const gatewayNetworks = Object.entries(gateway.networks ?? {});
  requireCondition(gatewayNetworks.length === 1, "gateway must use exactly one private application network");
  const [networkName, gatewayNetwork] = gatewayNetworks[0];
  const gatewayAddress = gatewayNetwork?.ipv4_address;
  requireCondition(typeof gatewayAddress === "string", "gateway must have a stable private IPv4 address");
  requireCondition(
    backendEnvironment.XBOARD_TRUSTED_PROXY_CIDRS === `${gatewayAddress}/32`,
    "backend must trust only the exact gateway address"
  );
  const network = config.networks?.[networkName];
  const subnets = list(network?.ipam?.config).map((entry) => entry.subnet).filter(Boolean);
  requireCondition(subnets.length === 1 && cidrContains(subnets[0], gatewayAddress), "gateway address must belong to the configured private subnet");

  for (const [name, service] of Object.entries(services)) {
    for (const port of list(service.ports)) {
      requireCondition(port.host_ip === "127.0.0.1", `${name} publishes a non-loopback host port`);
    }
  }

  requireCondition(gateway.depends_on?.backend?.condition === "service_healthy", "gateway must wait for a healthy backend");
  requireCondition(gateway.depends_on?.frontend?.condition === "service_healthy", "gateway must wait for a healthy frontend");
  return true;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const input = readFileSync(0, "utf8");
  validateSplitTopology(JSON.parse(input));
  process.stdout.write("split topology validation passed\n");
}
