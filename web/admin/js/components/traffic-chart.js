export function createStatusChart(canvas, labels, values) {
  if (!globalThis.Chart) return { destroy() {} };
  return new globalThis.Chart(canvas, { type: 'doughnut', data: { labels, datasets: [{ data: values, backgroundColor: ['#38d6a0', '#f4bd4f', '#55a8ff', '#ff6b7a', '#91a7c1'], borderWidth: 0 }] }, options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'bottom', labels: { color: '#91a7c1' } } } } });
}
