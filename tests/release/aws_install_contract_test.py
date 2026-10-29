#!/usr/bin/env python3
"""Focused checks for AWS wrapper and least-privilege SG plan contracts."""
import pathlib
import platform
import subprocess
import sys

root = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).parents[2])
scripts = root / "scripts"
for name in ("aws-rhel-preflight-ver20.sh", "install-controller-aws-ver20.sh", "install-client-aws-ver20.sh", "aws-security-group-plan-ver20.sh"):
    if platform.system() != "Windows":
        subprocess.run(["bash", "-n", str(scripts / name)], check=True)

plan_script = (scripts / "aws-security-group-plan-ver20.sh").read_text()
assert '"FromPort":12382' in plan_script and '"GroupId":"$client_sg"' in plan_script
assert '"CidrIp":"$admin_cidr"' in plan_script and '"FromPort":22' in plan_script
assert "0.0.0.0/0" in plan_script and "controller and client security groups must be distinct" in plan_script

controller = (scripts / "install-controller-aws-ver20.sh").read_text()
client = (scripts / "install-client-aws-ver20.sh").read_text()
assert "--mode 7" in controller and "NLI_SETUP_ENDPOINT" in controller
assert "chmod 600" in client and "--bootstrap" in client
print("AWS Red Hat installer contracts: PASS")
