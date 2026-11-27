// Shows each timestamp in the reader's own time zone and keeps the original, in
// UTC, as the tooltip. Nothing else on the page depends on this script: with it
// blocked, the times are still there, in UTC.
for (const el of document.querySelectorAll("time[datetime]")) {
  const when = new Date(el.getAttribute("datetime"));
  if (Number.isNaN(when.getTime())) continue;
  el.title = el.textContent;
  el.textContent = when.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}
