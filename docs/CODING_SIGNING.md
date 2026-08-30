# Windows Code Signing & EV Certificate Guide

This document outlines the architecture, procurement, storage, CI/CD integration, and verification procedures for Windows Extended Validation (EV) code signing in `g8s`.

---

## 1. Overview & Objectives

Windows SmartScreen and enterprise policies block unsigned binaries and MSI installers, presenting user-facing security warnings. Signing release artifacts with a trusted Extended Validation (EV) certificate:
- Establishes publisher identity (TamLD / The Gatekeepers).
- Eliminates Windows Defender SmartScreen warnings as download reputation accumulates (~50+ downloads).
- Prevents tampering and ensures binary integrity via SHA-256 signatures and RFC 3161 timestamps.

---

## 2. Certificate Procurement

### 2.1 Certificate Authority (CA) Selection
- **Providers**: DigiCert, Sectigo, or GlobalSign.
- **Certificate Type**: Extended Validation (EV) Code Signing Certificate.
- **Estimated Cost**: ~$300–$500/year.
- **Issuance Timeframe**: 1–2 weeks (requires business verification, Dun & Bradstreet validation, and identity checks).

### 2.2 Storage Model
- **Hardware Security Module (HSM)** / **Cloud HSM**:
  - Azure Key Vault (Managed HSM / Premium Key Vault) for automated CI/CD signing.
  - Physical YubiKey 5 FIPS hardware token for offline/air-gapped release signing.

---

## 3. Secret Management & CI Integration

### 3.1 Azure Key Vault & OIDC Setup
1. **Provision Key Vault**: Create an Azure Key Vault resource configured for certificate storage.
2. **Import Certificate**: Import the EV certificate (.pfx format) with non-exportable private key policies.
3. **Configure GitHub OIDC**:
   - Set up Azure Federated Credentials for the GitHub Actions repository (`tamld/g8s`).
   - Grant the GitHub Actions service principal `Key Vault Certificate User` and `Crypto Signer` roles.
4. **GitHub Encrypted Secrets & Environment Variables**:
   - `AZURE_CLIENT_ID`: Azure App Registration Client ID.
   - `AZURE_TENANT_ID`: Azure Tenant ID.
   - `AZURE_SUBSCRIPTION_ID`: Azure Subscription ID.
   - `AZURE_KEYVAULT_NAME`: Azure Key Vault Name.
   - `CERT_PATH`: Local or decrypted `.pfx` path (if using secret-based signing).
   - `HAS_CERT`: Set to `'true'` when signing secrets are available.

### 3.2 Fork Protection
- Pull requests from forks do NOT have access to signing credentials and will skip the signing step gracefully.
- Release tags executed from the main repository perform EV signing before artifact publication.

---

## 4. Signing & Verification Commands

### 4.1 Automated Signing via `signtool.exe`
```powershell
# Dual-sign / SHA-256 sign with DigiCert timestamp authority
signtool sign `
  /tr http://timestamp.digicert.com `
  /td sha256 `
  /fd sha256 `
  /a `
  /f $env:CERT_PATH `
  ./dist/*.exe ./dist/*.msi
```

### 4.2 Verification
```powershell
# Verify signature against Authenticode trust anchors
signtool verify /pa ./dist/*.exe
signtool verify /pa ./dist/*.msi
```

---

## 5. Manual Steps to Provision / Rotate Certificate

When renewing or rotating the EV certificate:
1. Generate a new CSR (Certificate Signing Request) via Azure Key Vault or HSM token.
2. Submit CSR to DigiCert/Sectigo and complete organization validation.
3. Download the issued `.pfx` / `.cer` chain.
4. Update the certificate in Azure Key Vault or update the base64-encoded secret in GitHub repository settings:
   ```bash
   base64 -i g8s-cert.pfx | pbcopy
   gh secret set WINDOWS_SIGNING_CERT_PFX
   ```
5. Run a test release verification against a staging tag to verify `signtool verify /pa` passes.
