export type PasskeySupport = "available" | "unavailable" | "ios-google-app";

export function isIOSGoogleApp(userAgent: string): boolean {
  return /\bGSA\//.test(userAgent) && /iPhone|iPad|iPod|Macintosh.*Mobile/.test(userAgent);
}

export async function passkeySupport(
  userAgent: string,
  hasCredentials: boolean,
  publicKeyCredential: Pick<typeof PublicKeyCredential, "isUserVerifyingPlatformAuthenticatorAvailable"> | undefined,
): Promise<PasskeySupport> {
  const googleApp = isIOSGoogleApp(userAgent);
  if (!hasCredentials || !publicKeyCredential) return googleApp ? "ios-google-app" : "unavailable";
  // WKWebView can expose the WebAuthn API without allowing this RP's
  // credentials. Only use the platform check to gate the iOS Google app;
  // other browsers may still support external security keys.
  if (googleApp) {
    try {
      if (!await publicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable()) return "ios-google-app";
    } catch { return "ios-google-app"; }
  }
  return "available";
}
