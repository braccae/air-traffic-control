import React, { useRef } from 'react';

/**
 * Raw YAML editing surface for a playbook.
 *
 * This is the swap-in seam for the visual (field-based) playbook editor: it owns
 * only "text in, text out", no playbook semantics, and both editors are expected
 * to drive the same `value` / `onChange` pair so a playbook round-trips between
 * the two representations unchanged.
 */
export interface PlaybookYamlEditorProps {
  value: string;
  onChange: (next: string) => void;
  readOnly?: boolean;
  /** Placeholder shown while the playbook is empty. */
  placeholder?: string;
}

export const PlaybookYamlEditor: React.FC<PlaybookYamlEditorProps> = ({
  value,
  onChange,
  readOnly = false,
  placeholder,
}) => {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const gutterRef = useRef<HTMLDivElement>(null);

  const lineCount = value ? value.split('\n').length : 1;
  const lineNumbers = Array.from({ length: Math.max(lineCount, 1) }, (_, i) => i + 1);

  const handleScroll = () => {
    if (textareaRef.current && gutterRef.current) {
      gutterRef.current.scrollTop = textareaRef.current.scrollTop;
    }
  };

  // Tab inserts two spaces (Ansible's native indentation) instead of moving focus.
  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== 'Tab' || readOnly) return;
    e.preventDefault();
    const textarea = textareaRef.current;
    if (!textarea) return;

    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const next = textarea.value.substring(0, start) + '  ' + textarea.value.substring(end);
    onChange(next);

    requestAnimationFrame(() => {
      textarea.selectionStart = textarea.selectionEnd = start + 2;
    });
  };

  return (
    <div className="flex-1 flex overflow-hidden relative min-h-[260px]" data-testid="playbook-yaml-editor">
      <div
        ref={gutterRef}
        className="w-12 bg-[#0c0f24] text-[#4e526b] font-mono text-xs leading-5 py-3 px-2 text-right select-none border-r border-[#26293c] overflow-hidden"
      >
        {lineNumbers.map(n => (
          <div key={n}>{n}</div>
        ))}
      </div>

      <textarea
        ref={textareaRef}
        value={value}
        onChange={e => onChange(e.target.value)}
        onScroll={handleScroll}
        onKeyDown={handleKeyDown}
        readOnly={readOnly}
        spellCheck={false}
        placeholder={placeholder}
        className="flex-1 h-full bg-[#0a0d1f] text-[#dfe1fa] font-mono text-xs leading-5 p-3 outline-none resize-none overflow-y-auto selection:bg-[#26293c]"
      />
    </div>
  );
};
