# FturnRaw component versions

Release v1.1.1 packages the client and server cores as 1.1.1. Its client starts from
1.1.0-captcha.2 and changes proactive TCP-flow migration. Client 1.1.1 is wire
compatible with server 1.1.0. Existing profiles, keys and device limits carry over.

Check `raw-client --version` and `raw-server --version`. Android and Windows
application versions are independent of the standalone core version.
