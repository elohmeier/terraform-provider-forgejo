#!/usr/bin/env python3
"""Run provider acceptance tests against a disposable, localhost-only Forgejo."""
import http.client
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
NAME = f"forgejo-provider-test-{os.getpid()}"
IMAGE = "codeberg.org/forgejo/forgejo:15.0.1-rootless@sha256:4f4d168b4e792d0f73e5f4da0548f3b54b9c9d03fb85f277c97eb985cb9a290a"


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def main():
    # These fixtures are not production credentials. Keep them in process memory.
    password = secrets.token_urlsafe(32)
    created = False
    try:
        run("docker", "create", "--name", NAME, "-p", "127.0.0.1:3000:3000",
            "-e", "FORGEJO__database__DB_TYPE=sqlite3",
            "-e", "FORGEJO__security__INSTALL_LOCK=true",
            "-e", "FORGEJO__server__OFFLINE_MODE=true",
            "-e", "FORGEJO__service__DISABLE_REGISTRATION=true",
            "-e", "FORGEJO__security__PASSWORD_COMPLEXITY=off",
            "-e", "FORGEJO__security__MIN_PASSWORD_LENGTH=1", IMAGE,
            stdout=subprocess.DEVNULL)
        created = True
        run("docker", "start", NAME, stdout=subprocess.DEVNULL)
        for _ in range(60):
            try:
                with urllib.request.urlopen("http://127.0.0.1:3000/api/healthz", timeout=2) as response:
                    if response.status == 200:
                        break
            except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.RemoteDisconnected):
                pass
            time.sleep(1)
        else:
            raise RuntimeError("Disposable Forgejo did not become healthy")
        run("docker", "exec", "-i", NAME, "sh", "-c",
            'read -r password; exec forgejo admin user create --username tfadmin '
            '--email tfadmin@localhost --password "$password" --admin --must-change-password=false',
            input=password + "\n", stdout=subprocess.DEVNULL)
        token = run("docker", "exec", NAME, "forgejo", "admin", "user", "generate-access-token",
                    "--username", "tfadmin", "--token-name", "acceptance", "--scopes", "all", "--raw",
                    stdout=subprocess.PIPE).stdout.strip()
        env = dict(os.environ, TF_ACC="1", FORGEJO_USERNAME="tfadmin", FORGEJO_PASSWORD=password,
                   FORGEJO_API_TOKEN=token, TF_ACC_PROVIDER_HOST="registry.opentofu.org",
                   TF_ACC_PROVIDER_NAMESPACE="hashicorp", TF_ACC_TERRAFORM_PATH=shutil.which("tofu") or "tofu")
        args = sys.argv[1:] or ["./internal/provider/", "-timeout", "20m"]
        return subprocess.call(["go", "test", *args], cwd=ROOT, env=env)
    finally:
        if created:
            subprocess.run(["docker", "rm", "-fv", NAME], check=False,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    sys.exit(main())
