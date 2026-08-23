import type { ReactNode } from "react";

export function ErrorNotice({ message }: { message: string }) {
  return <div className="alert alert-error" role="alert"><strong>Request failed</strong><span>{message}</span></div>;
}

export function EmptyState({ title, children, action }: { title: string; children: ReactNode; action?: ReactNode }) {
  return <div className="empty-state"><span className="empty-icon" aria-hidden="true">◇</span><h2>{title}</h2><p>{children}</p>{action}</div>;
}

export function LoadingRows({ count = 3 }: { count?: number }) {
  return <div className="loading-stack" aria-label="Loading">{Array.from({ length: count }, (_, index) => <div className="loading-row" key={index} />)}</div>;
}
