import React from 'react';

export interface ColumnDef<T> {
  key: string;
  header: string;
  className?: string;
  render?: (item: T) => React.ReactNode;
}

export interface ActionTrigger<T> {
  label: string;
  icon?: string;
  className?: string;
  disabled?: (item: T) => boolean;
  onClick: (item: T) => void;
}

export interface PluginResourceTableProps<T> {
  columns: ColumnDef<T>[];
  data: T[];
  keyExtractor: (item: T, index: number) => string;
  actions?: ActionTrigger<T>[];
  emptyMessage?: string;
  className?: string;
}

export function PluginResourceTable<T>({
  columns,
  data,
  keyExtractor,
  actions = [],
  emptyMessage = 'No resources found.',
  className = '',
}: PluginResourceTableProps<T>) {
  if (data.length === 0) {
    return (
      <div className={`bg-[#171a2d] border border-[#26293c] rounded p-6 text-center text-xs font-mono text-[#8d909b] ${className}`.trim()}>
        {emptyMessage}
      </div>
    );
  }

  return (
    <div className={`overflow-x-auto border border-[#26293c] rounded bg-[#171a2d] ${className}`.trim()}>
      <table className="w-full text-left font-mono text-xs">
        <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase border-b border-[#26293c]">
          <tr className="h-8">
            {columns.map(col => (
              <th key={col.key} className={`px-3 ${col.className || ''}`}>
                {col.header}
              </th>
            ))}
            {actions.length > 0 && (
              <th className="px-3 text-right">Actions</th>
            )}
          </tr>
        </thead>
        <tbody className="divide-y divide-[#26293c]/60">
          {data.map((item, index) => (
            <tr
              key={keyExtractor(item, index)}
              className="h-10 hover:bg-[#26293c]/40 transition-colors"
            >
              {columns.map(col => (
                <td key={col.key} className={`px-3 py-1.5 ${col.className || ''}`}>
                  {col.render ? col.render(item) : (item as any)[col.key]}
                </td>
              ))}
              {actions.length > 0 && (
                <td className="px-3 py-1.5 text-right whitespace-nowrap">
                  <div className="flex items-center justify-end gap-1.5">
                    {actions.map((act, actIdx) => {
                      const isDisabled = act.disabled ? act.disabled(item) : false;
                      return (
                        <button
                          key={actIdx}
                          type="button"
                          disabled={isDisabled}
                          onClick={() => act.onClick(item)}
                          className={
                            act.className ||
                            'flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2 py-0.5 rounded text-[11px] transition-colors border border-[#434750]/40 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed'
                          }
                        >
                          {act.icon && (
                            <span className="material-symbols-outlined text-[13px]">{act.icon}</span>
                          )}
                          <span>{act.label}</span>
                        </button>
                      );
                    })}
                  </div>
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
