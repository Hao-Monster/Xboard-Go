const paths: Record<string, string> = {
  system: "M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z",
  settings: "M4 7h16 M4 17h16 M8 4v6 M16 14v6",
  plugins: "M8 3v4 M16 3v4 M6 7h12v4a6 6 0 0 1-12 0z M12 17v4",
  themes: "M12 3a9 9 0 1 0 0 18h2a2 2 0 0 0 0-4h-1a2 2 0 0 1 0-4h3a5 5 0 0 0 0-10z M7 9h.01 M11 6h.01 M16 7h.01",
  notices: "M3 10h5l11-5v14L8 14H3z M7 14l2 7h3",
  payments: "M3 5h18v14H3z M3 10h18 M7 15h3",
  knowledge: "M3 4h7l2 2 2-2h7v16h-7l-2 1-2-1H3z M12 6v15",
  clients: "M7 2h10v20H7z M11 18h2",
  servers: "M3 3h18v7H3z M3 14h18v7H3z M7 6h.01 M7 17h.01",
  nodes: "M12 3v6 M5 15V9h14v6 M3 15h4v6H3z M17 15h4v6h-4z M10 3h4v4h-4z",
  groups: "M12 3l8 4v6c0 4-8 8-8 8s-8-4-8-8V7z M9 12l2 2 4-4",
  routes: "M4 4h6v6H4z M14 14h6v6h-6z M17 14V7h-7 M7 10v7h7",
  plans: "M5 3h14v18H5z M9 7h6 M9 12h6 M9 17h4",
  orders: "M5 3h14v18l-3-2-4 2-4-2-3 2z M8 7h8 M8 11h8 M8 15h5",
  distributors: "M3 8h18v13H3z M8 8V3h8v5 M3 12h18 M10 12v3h4v-3",
  coupons: "M3 5h18v5a2 2 0 0 0 0 4v5H3v-5a2 2 0 0 0 0-4z M15 5v14",
  "gift-cards": "M3 8h18v4H3z M5 12v9h14v-9 M12 8v13 M12 8C3 8 5 1 9 3l3 5c9 0 7-7 3-5z",
  users: "M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8 M2 21v-3a7 7 0 0 1 14 0v3 M17 4a4 4 0 0 1 0 8 M19 15a5 5 0 0 1 3 6",
  tickets: "M4 4h16v13H9l-5 4z M8 8h8 M8 12h5"
};

export function AdminNavIcon({ page }: { page: string }) {
  return <svg className="admin-nav-icon" aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d={paths[page] ?? paths.settings} /></svg>;
}
