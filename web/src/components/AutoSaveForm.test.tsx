import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState, type FormEvent } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { AutoSaveForm } from "./AutoSaveForm";

afterEach(() => vi.useRealTimers());

it("saves the latest edit once after one second, respects validation and locks during requests", async () => {
  vi.useFakeTimers();
  const save = vi.fn();
  function Example() {
    const [value, setValue] = useState("");
    const [saving, setSaving] = useState(false);
    return <AutoSaveForm saving={saving} onSubmit={event => { event.preventDefault(); save(value); setSaving(true); }}>
      <input aria-label="站点名称" required value={value} onChange={event => setValue(event.target.value)} />
      <button type="submit">保存</button>
    </AutoSaveForm>;
  }
  render(<Example />);
  const field = screen.getByRole("textbox");
  fireEvent.change(field, { target: { value: "A" } });
  await act(() => vi.advanceTimersByTimeAsync(800));
  fireEvent.change(field, { target: { value: "AB" } });
  await act(() => vi.advanceTimersByTimeAsync(999));
  expect(save).not.toHaveBeenCalled();
  await act(() => vi.advanceTimersByTimeAsync(1));
  expect(save).toHaveBeenCalledExactlyOnceWith("AB");
  expect(field).toBeDisabled();
});

it("does not submit invalid fields or after navigating away", async () => {
  vi.useFakeTimers(); const save = vi.fn((event: FormEvent<HTMLFormElement>) => event.preventDefault());
  const { unmount } = render(<AutoSaveForm saving={false} onSubmit={save}><input aria-label="URL" type="url" required /></AutoSaveForm>);
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "invalid" } });
  await act(() => vi.advanceTimersByTimeAsync(1000));
  expect(save).not.toHaveBeenCalled();
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "https://example.test" } });
  unmount();
  await act(() => vi.advanceTimersByTimeAsync(1000));
  expect(save).not.toHaveBeenCalled();
});
