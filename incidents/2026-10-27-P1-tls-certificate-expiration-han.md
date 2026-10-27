---
id: "d90121443cda2c51"
title: "TLS Certificate Expiration & Handshake Failure"
severity: "P1"
status: "OPEN"
timestamp: "2026-10-27T03:34:43Z"
namespace: ""
signatures:
  - "TLS Certificate Expiration / Handshake Rejection"
  - "Container Crash / CrashLoopBackOff"
---

# Incident Postmortem: TLS Certificate Expiration & Handshake Failure

- **Incident ID:** `d90121443cda2c51`
- **Severity:** `P1`
- **Reported At:** 2026-10-27 03:34:43 UTC

---

## 1. Executive Summary & Root Cause

> **Root Cause:** x509 certificate expired past validity timestamp or CA cert trust store mismatch.

**Initial Symptom:** Discovered in log file /home/vj/code/log_analyzer/testdata/logs/06-tls-certificate-expiry.log

### Detected Failure Signatures

- ⚠️ TLS Certificate Expiration / Handshake Rejection
- ⚠️ Container Crash / CrashLoopBackOff

## 2. Full Multi-Agent Diagnostic Report

## Autonomous Log Analysis
- **Diagnosed Issue:** TLS Certificate Expiration & Handshake Failure
- **Severity:** P1
- **Root Cause Deduction:** x509 certificate expired past validity timestamp or CA cert trust store mismatch.

### Remediation Guidance
1. Renew the expired TLS/mTLS certificate in cert-manager or secrets vault.
1. Verify certificate validity dates with 'openssl x509 -in tls.crt -noout -dates'.
1. Restart pods to pick up renewed TLS secrets if certificates are not auto-reloaded.


## 3. Log Excerpt Evidence

```text
2026-10-27T08:10:00.010Z [INFO] auth-gateway: TLS listener initialized with certificate chain /etc/ssl/certs/tls.crt
2026-10-27T08:10:02.120Z [INFO] auth-gateway: Establishing secure mutual TLS link to user-identity-vault:8443
2026-10-27T08:10:05.450Z [ERROR] auth-gateway: Transport: authentication handshake failed: x509: certificate has expired or is not yet valid: current time 2026-10-27T08:10:05Z is after 2026-10-26T23:59:59Z
2026-10-27T08:10:06.100Z [ERROR] auth-gateway: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: remote error: tls: bad certificate"
2026-10-27T08:10:07.210Z [ERROR] auth-gateway: Failed to verify identity token for request req_90123: x509: certificate has expired or is not yet valid
2026-10-27T08:10:08.330Z [WARN] auth-gateway: Retrying TLS handshake to user-identity-vault:8443 (attempt 2/5)
2026-10-27T08:10:09.110Z [ERROR] auth-gateway: Transport: authentication handshake failed: x509: certificate has expired or is not yet valid
2026-10-27T08:10:10.001Z [FATAL] auth-gateway: Security policy enforcement failed: all mTLS handshakes rejected by cluster PKI
2026-10-27T08:10:10.100Z kubelet: Container auth-gateway terminated with exit code 1
```
