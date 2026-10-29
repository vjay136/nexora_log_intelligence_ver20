# V20 AWS and Red Hat installation

The AWS path is additive to the existing V20 guided, local, private TLS, corporate PKI, public DNS, and Cloudflare modes. The current RPMs and setup utilities keep their existing paths; the V20 wrappers apply AWS preflight and safe private deployment defaults around them.

## Supported hosts and upgrade behavior

The wrappers support x86_64 RHEL and Rocky Linux 8, 9, and 10 with DNF, RPM, systemd, Python 3, curl, and OpenSSL. Run the host check before installing:

```bash
sudo bash scripts/aws-rhel-preflight-ver20.sh
```

Install or upgrade the controller from a complete, checksum-verified release bundle:

```bash
sudo bash scripts/install-controller-aws-ver20.sh ./nli_controller_agent_ver20.rpm 10.20.1.10
```

The endpoint must be a private IPv4 address or a private DNS name. The wrapper selects offline private TLS (generated CA, no public DNS/ACME) and local storage, preserves existing state during RPM upgrades, and creates the root-only initial admin password file when needed. After setup, retrieve `/root/nli-client-bootstrap.env` over an approved secure channel. This file carries a one-use enrollment token and the private CA; treat it as a credential, copy it only to its intended client, and remove temporary copies after enrollment.

Install/enroll a client using explicit RPM and bootstrap paths:

```bash
chmod 600 ./nli-client-bootstrap.env
sudo bash scripts/install-client-aws-ver20.sh ./nli_client_ver20.rpm ./nli-client-bootstrap.env --automatic
```

The wrapper rejects group/world-readable bootstrap files. Successful setup consumes the one-use token, stores the issued client token with restricted permissions, and starts the existing systemd services. Re-running an RPM install is an upgrade; setup does not erase controller or client state. Existing installations retain their installed configuration and enrollment.

## AWS security group policy

Put controller and clients in private subnets and assign distinct security groups. Generate a reviewable AWS CLI plan:

```bash
bash scripts/aws-security-group-plan-ver20.sh sg-CONTROLLER sg-CLIENT 198.51.100.24/32
```

The plan allows TCP 12382 on the controller only from the client security group, and SSH TCP 22 only from the supplied administrator IPv4 CIDR. It never creates public TCP 12382. Review and apply the printed commands using an AWS principal restricted to the intended security groups. Apply equivalent subnet NACL and host firewall policy when those controls are used. The controller setup may add its service port to the host firewalld zone; the AWS security group is the required network source restriction. Avoid attaching a broad public-facing security group to the controller.

For Cloudflare Tunnel deployments, use the existing Cloudflare mode and keep the tunnel outbound-only. Permit controller egress TCP 443 and 7844 to Cloudflare's currently published destinations, preferably through managed prefix lists or an egress proxy. Do not expose inbound TCP 12382 for the tunnel path. The private-TLS direct client path and Cloudflare Tunnel path are separate deployment choices.

## Preflight and diagnostics

Before installing, confirm controller and client instances share VPC routing and that the selected endpoint resolves to the private address. After setup, check:

```bash
sudo systemctl status nli-controller-agent nli-client-control nli-agent
sudo journalctl -u nli-controller-agent -u nli-client-control --since today
sudo ss -lntp | grep 12382
sudo firewall-cmd --list-all
curl --cacert /var/lib/nli-controller/tls/ca.crt https://CONTROLLER_PRIVATE_IP:12382/api/v1/health
```

From a client, test private routing and TCP reachability with `ip route get CONTROLLER_PRIVATE_IP` and `curl --cacert /etc/nli/controller-ca.crt https://CONTROLLER_PRIVATE_IP:12382/api/v1/health`. A timeout usually indicates route, NACL, security group, or firewalld policy. A refusal points to the controller service/listener. A certificate error indicates a stale/wrong bootstrap or an endpoint identity mismatch; recopy a fresh bootstrap rather than disabling TLS verification. Inspect `journalctl -u nli-client-control` and `journalctl -u nli-controller-agent` for enrollment/API detail. The AWS wrappers do not accept or print passwords, enrollment tokens, or other secret values.
