#!/usr/bin/env python3
"""Provision only the local synthetic browser client; default is PLAN."""
from __future__ import annotations

import argparse
import copy
import json
import os
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "eval"))
from run_keycloak_protocol import HTTP, protected_json


def run(args):
    accounts = protected_json(args.accounts_file)
    config = protected_json(args.oidc_config)
    if config["issuer"] != "http://127.0.0.1:18095/realms/ecr-v2-test" or accounts["realm"] != "ecr-v2-test":
        raise ValueError("dedicated_local_realm_required")
    if args.output_config.exists() or args.output_secret.exists():
        raise ValueError("output_already_exists")
    client = HTTP("http://127.0.0.1:18095")
    code, result = client.call("POST", "/realms/master/protocol/openid-connect/token", {
        "grant_type": "password", "client_id": "admin-cli", "username": accounts["admin_username"],
        "password": accounts["admin_password"],
    }, form=True)
    if code != 200:
        raise ValueError("local_admin_login_failed")
    admin = result["access_token"]
    prefix = "/admin/realms/ecr-v2-test/clients"
    code, previous = client.call("GET", prefix + "?clientId=" + accounts["client_id"], token=admin)
    if code != 200 or len(previous) != 1:
        raise ValueError("existing_protocol_client_missing")
    original = previous[0]
    mappers = copy.deepcopy(original.get("protocolMappers", []))
    for mapper in mappers:
        mapper.pop("id", None)
        if mapper.get("protocolMapper") == "oidc-audience-mapper":
            mapper["config"]["included.client.audience"] = "ecr-v2-browser"
    desired = {
        "clientId": "ecr-v2-browser", "protocol": "openid-connect", "enabled": True,
        "publicClient": False, "clientAuthenticatorType": "client-secret",
        "standardFlowEnabled": True, "directAccessGrantsEnabled": False,
        "implicitFlowEnabled": False, "serviceAccountsEnabled": False,
        "redirectUris": ["http://127.0.0.1:5173/v1/auth/callback"], "webOrigins": [],
        "attributes": {"pkce.code.challenge.method": "S256", "access.token.lifespan": "120"},
        "defaultClientScopes": original.get("defaultClientScopes", []), "protocolMappers": mappers,
    }
    code, existing = client.call("GET", prefix + "?clientId=ecr-v2-browser", token=admin)
    if code != 200 or len(existing) > 1:
        raise ValueError("browser_client_lookup_failed")
    if existing:
        desired["id"] = existing[0]["id"]
        code, _ = client.call("PUT", prefix + "/" + desired["id"], desired, token=admin)
    else:
        code, _ = client.call("POST", prefix, desired, token=admin)
    if code not in {201, 204}:
        raise ValueError("browser_client_provision_failed")
    code, created = client.call("GET", prefix + "?clientId=ecr-v2-browser", token=admin)
    if code != 200 or len(created) != 1:
        raise ValueError("browser_client_verification_failed")
    selected = created[0]
    if selected.get("directAccessGrantsEnabled") or not selected.get("standardFlowEnabled"):
        raise ValueError("browser_client_grants_invalid")
    code, secret = client.call("GET", prefix + "/" + selected["id"] + "/client-secret", token=admin)
    if code != 200 or not secret.get("value"):
        raise ValueError("browser_secret_unavailable")
    config.update(client_id="ecr-v2-browser", secret_file=str(args.output_secret))
    config["browser"] = {"customer_origin": "http://127.0.0.1:5173", "admin_origin": "http://127.0.0.1:5174",
                         "callback_url": "http://127.0.0.1:5173/v1/auth/callback", "allow_loopback_http": True}
    for path, data in ((args.output_secret, secret["value"]), (args.output_config, json.dumps(config))):
        if not path.is_absolute() or path.parent.is_symlink():
            raise ValueError("protected_output_path_required")
        with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as output:
            output.write(data)
    return {"status": "PASS", "client_id": "ecr-v2-browser", "password_grant": False,
            "pkce": "S256", "real_service_acceptance": False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true")
    for key in ("accounts-file", "oidc-config", "output-config", "output-secret"):
        parser.add_argument("--" + key, type=Path)
    args = parser.parse_args()
    if not args.live:
        print(json.dumps({"status": "PLAN", "client_id": "ecr-v2-browser", "real_service_acceptance": False}))
        return 0
    try:
        if any(getattr(args, k.replace("-", "_")) is None for k in ("accounts-file", "oidc-config", "output-config", "output-secret")):
            raise ValueError("protected_inputs_required")
        print(json.dumps(run(args)))
        return 0
    except Exception:  # noqa: BLE001 - do not expose protected transport inputs
        print(json.dumps({"status": "NOT_PASSED", "failure_code": "local_browser_provision_failed"}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
