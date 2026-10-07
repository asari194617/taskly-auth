// Existing links and login flows still use /passkeys. The settings UI now lives
// under /security, so keep this public URL as a compatibility entry point.
location.replace("/security?section=passkeys");
