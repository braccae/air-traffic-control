import React, { useEffect } from 'react';

export interface ToastItem {
  id: string;
  type: 'info' | 'success' | 'warning' | 'error';
  title: string;
  message?: string;
  timestamp?: number;
}

interface ToastNotificationProps {
  toasts: ToastItem[];
  onDismiss: (id: string) => void;
}

export const ToastNotification: React.FC<ToastNotificationProps> = ({ toasts, onDismiss }) => {
  useEffect(() => {
    if (toasts.length === 0) return;
    const timers = toasts.map(toast => {
      return setTimeout(() => {
        onDismiss(toast.id);
      }, 4500);
    });
    return () => {
      timers.forEach(t => clearTimeout(t));
    };
  }, [toasts, onDismiss]);

  if (toasts.length === 0) return null;

  return (
    <div className="fixed top-12 right-4 z-50 flex flex-col gap-2 max-w-sm w-full pointer-events-none">
      {toasts.map(toast => {
        const isSuccess = toast.type === 'success';
        const isError = toast.type === 'error';
        const isWarning = toast.type === 'warning';

        return (
          <div
            key={toast.id}
            className={`pointer-events-auto p-3 rounded-lg shadow-xl border flex items-start gap-2.5 transition-all duration-300 transform translate-y-0 backdrop-blur-md ${
              isSuccess
                ? 'bg-[#0a1a17]/90 border-[#8cd6cb]/40 text-[#8cd6cb]'
                : isError
                ? 'bg-[#200d11]/90 border-[#ffb4ab]/40 text-[#ffb4ab]'
                : isWarning
                ? 'bg-[#1e1708]/90 border-[#ffd97d]/40 text-[#ffd97d]'
                : 'bg-[#101528]/90 border-[#b0c9ff]/40 text-[#b0c9ff]'
            }`}
          >
            <span className="material-symbols-outlined text-[18px] shrink-0 mt-0.5">
              {isSuccess ? 'check_circle' : isError ? 'error' : isWarning ? 'warning' : 'info'}
            </span>
            <div className="flex-1 font-mono text-xs">
              <div className="font-bold text-[#dfe1fa]">{toast.title}</div>
              {toast.message && <div className="text-[11px] text-[#c3c6d2] mt-0.5">{toast.message}</div>}
            </div>
            <button
              type="button"
              onClick={() => onDismiss(toast.id)}
              className="text-[#8d909b] hover:text-[#dfe1fa] p-0.5 cursor-pointer text-xs leading-none"
            >
              ✕
            </button>
          </div>
        );
      })}
    </div>
  );
};
