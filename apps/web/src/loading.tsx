import { LoadingSpinner } from "@charcoal-ui/react";
import type { ReactNode } from "react";

export function LoadingStatus({ children }: { children: string }) {
  return <div className="account-loading" role="status" aria-label={children}>
    <span aria-hidden="true"><LoadingSpinner size={24} padding={0} transparent /></span>
  </div>;
}

export function BusyLabel({ busy, children }: { busy: boolean; children: ReactNode }) {
  if (!busy) return <>{children}</>;
  return <span className="busy-label">
    <span aria-hidden="true"><LoadingSpinner size={18} padding={0} transparent /></span>
    <span>{children}</span>
  </span>;
}
