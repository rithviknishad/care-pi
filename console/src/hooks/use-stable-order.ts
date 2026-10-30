import { useState } from "react";

// Keeps the first order of a list across refreshes: a known item keeps its place,
// a new item goes to the end, and a removed item goes away. The list never reorders.
export function stableOrder<T>(
  previous: string[],
  items: T[],
  key: (item: T) => string,
): { order: string[]; items: T[] } {
  const byKey = new Map(items.map((item) => [key(item), item]));
  const order = previous.filter((k) => byKey.has(k));
  for (const k of byKey.keys()) if (!order.includes(k)) order.push(k);
  return { order, items: order.map((k) => byKey.get(k) as T) };
}

function same(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((k, i) => k === b[i]);
}

export function useStableOrder<T>(items: T[] | undefined, key: (item: T) => string): T[] | undefined {
  const [order, setOrder] = useState<string[]>([]);
  if (!items) return undefined;
  const result = stableOrder(order, items, key);
  // Store the new order during render (React renders this component again at once).
  if (!same(result.order, order)) setOrder(result.order);
  return result.items;
}
