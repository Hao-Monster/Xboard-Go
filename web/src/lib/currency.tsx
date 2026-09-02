import { createContext, useContext, type ReactNode } from "react";

interface CurrencyContextValue {
  code: string;
  symbol: string;
}

const CurrencyContext = createContext<CurrencyContextValue>({ code: "CNY", symbol: "¥" });

export function CurrencyProvider({ code, symbol, children }: CurrencyContextValue & { children: ReactNode }) {
  return <CurrencyContext.Provider value={{ code, symbol }}>{children}</CurrencyContext.Provider>;
}

export function useCurrency() {
  const currency = useContext(CurrencyContext);
  return {
    ...currency,
    format: (cents: number, locale = "zh-CN") => `${currency.symbol}${new Intl.NumberFormat(locale, {
      minimumFractionDigits: 2, maximumFractionDigits: 2
    }).format(cents / 100)}`
  };
}

export function formatCurrencyCents(cents: number, currency: string, locale = "zh-CN") {
  return new Intl.NumberFormat(locale, { style: "currency", currency }).format(cents / 100);
}
