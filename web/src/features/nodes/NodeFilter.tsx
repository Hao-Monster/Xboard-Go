import { useEffect, useMemo, useRef, useState } from "react";
import "./node-filter.css";

export function NodeFilter({
  label,
  options,
  value,
  onChange,
  disabled = false,
}: {
  label: string;
  options: Array<{ value: string; label: string }>;
  value: string[];
  onChange: (value: string[]) => void;
  disabled?: boolean;
}) {
  const [isOpen, setIsOpen] = useState(false);
  const [search, setSearch] = useState("");
  const containerRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!isOpen) {
      return;
    }

    searchInputRef.current?.focus();

    const handleOutsideClick = (event: MouseEvent | PointerEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsOpen(false);
        buttonRef.current?.focus();
      }
    };

    document.addEventListener("mousedown", handleOutsideClick);
    document.addEventListener("pointerdown", handleOutsideClick);
    document.addEventListener("keydown", handleKeyDown);

    return () => {
      document.removeEventListener("mousedown", handleOutsideClick);
      document.removeEventListener("pointerdown", handleOutsideClick);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [isOpen]);

  const filteredOptions = useMemo(() => {
    const trimmed = search.trim().toLowerCase();
    if (!trimmed) return options;
    return options.filter(
      (opt) =>
        opt.label.toLowerCase().includes(trimmed) ||
        opt.value.toLowerCase().includes(trimmed)
    );
  }, [options, search]);

  const handleToggle = (optValue: string) => {
    if (value.includes(optValue)) {
      onChange(value.filter((v) => v !== optValue));
    } else {
      onChange([...value, optValue]);
    }
  };

  const handleClear = () => {
    onChange([]);
  };

  return (
    <div className="node-filter" ref={containerRef}>
      <button
        ref={buttonRef}
        type="button"
        className="node-filter-button"
        aria-label={label}
        aria-expanded={isOpen}
        aria-haspopup="dialog"
        disabled={disabled}
        onClick={() => {setSearch("");setIsOpen((prev) => !prev);}}
      >
        <span className="node-filter-button-label">{label}</span>
        {value.length > 0 && (
          <span className="node-filter-button-count">{value.length}</span>
        )}
      </button>

      {isOpen && !disabled && (
        <div
          role="dialog"
          aria-label={`${label}筛选`}
          className="node-filter-dialog"
          tabIndex={-1}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.stopPropagation();
              setIsOpen(false);
              buttonRef.current?.focus();
            }
          }}
        >
          <div className="node-filter-search-container">
            <input
              ref={searchInputRef}
              type="text"
              className="node-filter-search-input"
              aria-label={`搜索${label}`}
              placeholder={`搜索${label}`}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>

          <div className="node-filter-options-list" role="group" aria-label={label}>
            {filteredOptions.length === 0 ? (
              <div className="node-filter-empty">无匹配选项</div>
            ) : (
              filteredOptions.map((option) => {
                const checked = value.includes(option.value);
                return (
                  <label key={option.value} className="node-filter-option">
                    <input
                      type="checkbox"
                      className="node-filter-checkbox"
                      checked={checked}
                      onChange={() => handleToggle(option.value)}
                      aria-label={option.label}
                    />
                    <span className="node-filter-option-label">{option.label}</span>
                  </label>
                );
              })
            )}
          </div>

          {value.length > 0 && (
            <div className="node-filter-footer">
              <button
                type="button"
                className="node-filter-clear-button"
                onClick={handleClear}
              >
                Clear filters
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
