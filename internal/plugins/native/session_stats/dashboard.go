package sessionstats

// dashboardHTML is a self-contained port of session-stats.py's Jinja
// TEMPLATE constant: the same stat cards, trend charts (via the shared
// chart.min.js already served at /js/plugins/chart.min.js by the built-in
// web server for other plugins' pages), and client-side fetch/rendering
// logic, byte-for-byte equivalent in behavior. It does not extend the
// shared base.html layout (see OnWebhook's doc comment for why that's
// out of this package's scope) so a small :root fallback palette
// replaces the CSS custom properties base.html would otherwise supply.
const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Session Stats</title>
<style>
:root {
  --border-color: #333;
  --card-bg: #1a1a1a;
  --accent: #4caf50;
  --accent-r: 76;
  --accent-g: 175;
  --accent-b: 80;
  --text-muted: #999;
  --font-pixel: monospace;
  --font-main: sans-serif;
  --shadow-md: 0 2px 8px rgba(0,0,0,0.4);
}
/* This page renders standalone rather than through base.html (see
   OnWebhook's doc comment), so it never inherits base.html's box-sizing
   reset. Without it, .chart's width:100% plus its padding+border are
   added on top of the grid column's share instead of inside it, making
   each chart ~34px wider than its column and overlapping its neighbor
   whenever the grid lays out 2+ columns side by side. */
*, *::before, *::after { box-sizing: border-box; }
body { background: #111; color: #eee; font-family: var(--font-main); margin: 0; padding: 1.5rem; }
.stats-header {
    margin-bottom: 2rem;
    padding: 1.5rem 0;
    border-bottom: 1px solid var(--border-color);
}
.session-selector {
    display: flex;
    gap: 1rem;
    align-items: center;
    margin-bottom: 2rem;
    background-color: var(--card-bg);
    padding: 1rem;
    border-radius: 8px;
    border: 1px solid var(--border-color);
}
.session-selector label {
    display: inline;
    font-size: 0.9rem;
    color: var(--accent);
    font-weight: 600;
    text-transform: uppercase;
    margin: 0;
    font-family: var(--font-pixel);
}
#session { flex: 1; min-width: 200px; }
.stats-container {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: 1rem;
    margin-bottom: 3rem;
}
.stat-card {
    background-color: var(--card-bg);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 1.5rem;
    text-align: center;
    transition: all 0.3s ease;
    box-shadow: var(--shadow-md);
}
.stat-card:hover {
    border-color: var(--accent);
    transform: translateY(-3px);
    box-shadow: 0 6px 20px rgba(var(--accent-r), var(--accent-g), var(--accent-b), 0.1);
}
.stat-label {
    font-size: 0.85rem;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    font-family: var(--font-pixel);
    font-weight: 600;
    margin-bottom: 0.5rem;
}
.stat-value {
    font-size: 2.2rem;
    font-weight: bold;
    color: var(--accent);
    font-family: var(--font-pixel);
    line-height: 1;
    letter-spacing: 1px;
}
.charts-section { margin-top: 3rem; }
.charts-section h3 {
    margin: 0 0 2rem 0;
    color: var(--accent);
    font-family: var(--font-pixel);
    font-size: 1.4rem;
    text-transform: uppercase;
    letter-spacing: 1px;
    padding-bottom: 1rem;
    border-bottom: 1px solid var(--border-color);
}
.charts-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(500px, 1fr));
    gap: 2rem;
}
div.chart {
    height: 300px;
    width: 100%;
    position: relative;
    background-color: var(--card-bg);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 1rem;
    box-shadow: var(--shadow-md);
    transition: all 0.3s ease;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    min-width: 0;
}
div.chart:hover {
    border-color: var(--accent);
    box-shadow: 0 8px 25px rgba(var(--accent-r), var(--accent-g), var(--accent-b), 0.1);
}
/* Title and legend are kept OUTSIDE the horizontally-scrollable canvas
   wrapper on purpose: Chart.js centers its own title/legend plugins
   relative to the full canvas width, and that canvas is deliberately made
   wider than the visible box so long sessions can be scrolled through. If
   title/legend rendered inside the canvas, they'd land off-screen at
   scrollLeft=0 for any chart with more than a screenful of points. */
.chart-title {
    font-family: var(--font-pixel);
    font-weight: 600;
    font-size: 1rem;
    color: #fff;
    text-align: center;
    margin-bottom: 0.5rem;
    flex: none;
}
.chart-scroll {
    flex: 1 1 auto;
    min-height: 0;
    min-width: 0;
    overflow-x: auto;
    overflow-y: hidden;
}
.chart-scroll canvas { display: block; height: 100%; min-width: 100%; }
.chart-legend {
    flex: none;
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 0.25rem 1rem;
    margin-top: 0.5rem;
    font-family: var(--font-main);
    font-size: 0.75rem;
    color: #fff;
}
.chart-legend .legend-item { display: flex; align-items: center; gap: 0.35rem; }
.chart-legend .legend-swatch { width: 12px; height: 4px; border-radius: 2px; display: inline-block; }
.chart-hint {
    font-size: 0.75rem;
    color: var(--text-muted);
    text-align: center;
    margin-top: 0.5rem;
    flex: none;
    font-family: var(--font-main);
}
@media (max-width: 768px) {
    .stats-container { grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); }
    .stat-card { padding: 1rem; }
    .stat-value { font-size: 1.8rem; }
    .stat-label { font-size: 0.8rem; }
    .charts-grid { grid-template-columns: 1fr; }
    div.chart { height: 250px; }
}
@media (max-width: 480px) {
    .session-selector { flex-direction: column; align-items: stretch; }
    .session-selector label { display: block; margin-bottom: 0.5rem; }
    #session { width: 100%; }
    .stats-container { grid-template-columns: 1fr; gap: 0.75rem; }
    .stat-card { padding: 0.75rem; }
    .stat-value { font-size: 1.5rem; }
    .stat-label { font-size: 0.75rem; }
    .charts-grid { gap: 1rem; }
    div.chart { height: 200px; padding: 0.75rem; }
}
</style>
</head>
<body>
<div class="stats-header">
    <h2>Session Statistics</h2>
    <p>Real-time monitoring of WiFi capture metrics and system performance</p>
</div>

<div class="session-selector">
    <label for="session">Session:</label>
    <select id="session">
        <option selected>Current</option>
    </select>
</div>

<div class="stats-container">
    <div class="stat-card">
        <div class="stat-label">Networks Captured</div>
        <div class="stat-value" id="stat_networks">0</div>
    </div>
    <div class="stat-card">
        <div class="stat-label">Handshakes</div>
        <div class="stat-value" id="stat_handshakes">0</div>
    </div>
    <div class="stat-card">
        <div class="stat-label">Deauths Sent</div>
        <div class="stat-value" id="stat_deauths">0</div>
    </div>
    <div class="stat-card">
        <div class="stat-label">Session Duration</div>
        <div class="stat-value" id="stat_duration">0s</div>
    </div>
    <div class="stat-card">
        <div class="stat-label">Temperature</div>
        <div class="stat-value" id="stat_temp">0°C</div>
    </div>
    <div class="stat-card">
        <div class="stat-label">Memory Usage</div>
        <div class="stat-value" id="stat_mem">0%</div>
    </div>
    <div class="stat-card">
        <div class="stat-label">CPU Load</div>
        <div class="stat-value" id="stat_cpu">0%</div>
    </div>
</div>

<div class="charts-section">
    <h3>Trend Charts</h3>
    <div class="charts-grid">
        <div id="chart_networks" class="chart"></div>
        <div id="chart_handshakes" class="chart"></div>
        <div id="chart_deauths" class="chart"></div>
        <div id="chart_temp" class="chart"></div>
        <div id="chart_mem" class="chart"></div>
        <div id="chart_cpu" class="chart"></div>
    </div>
</div>

<script src="/js/plugins/chart.min.js"></script>
<script>
    const charts = {};

    async function fetchData(url) {
        try {
            const response = await fetch(url);
            if (!response.ok) throw new Error("HTTP " + response.status);
            return await response.json();
        } catch (error) {
            console.error("Failed to fetch " + url + ":", error);
            return { values: [], labels: [] };
        }
    }

    function getTransparentColor(color) {
        if (color.startsWith('rgb(')) {
            return color.replace('rgb(', 'rgba(').replace(')', ', 0.2)');
        }
        return color + '33';
    }

    function createChart(elementId, title, data) {
        const container = document.getElementById(elementId);
        if (!container || !data.values || data.values.length === 0) return;

        if (charts[elementId]) charts[elementId].destroy();

        const allLabels = new Set();
        data.values.forEach(values => {
            values.forEach(([ts]) => allLabels.add(ts));
        });
        const labels = Array.from(allLabels).sort();

        const datasets = data.values.map((values, index) => {
            const color = getChartColor(index);
            const valueMap = Object.fromEntries(values);
            const chartData = labels.map(ts => valueMap[ts] ?? null);

            return {
                label: data.labels[index],
                data: chartData,
                borderColor: color,
                backgroundColor: getTransparentColor(color),
                borderWidth: 2,
                fill: true,
                tension: 0.1,
                pointRadius: 1,
                pointHoverRadius: 4
            };
        });

        // Title and legend are plain, non-scrolling DOM elements (see the
        // .chart-title/.chart-scroll/.chart-legend doc comment in <style>):
        // Chart.js's own title/legend plugins center on the full canvas,
        // which is wider than the visible box, so they'd render off-screen.
        let titleEl = container.querySelector('.chart-title');
        if (!titleEl) {
            titleEl = document.createElement('div');
            titleEl.className = 'chart-title';
            container.appendChild(titleEl);
        }
        titleEl.textContent = title;

        let scrollEl = container.querySelector('.chart-scroll');
        if (!scrollEl) {
            scrollEl = document.createElement('div');
            scrollEl.className = 'chart-scroll';
            container.appendChild(scrollEl);
        }

        let canvas = scrollEl.querySelector('canvas');
        if (!canvas) {
            canvas = document.createElement('canvas');
            scrollEl.appendChild(canvas);
        }

        let legendEl = container.querySelector('.chart-legend');
        if (!legendEl) {
            legendEl = document.createElement('div');
            legendEl.className = 'chart-legend';
            container.appendChild(legendEl);
        }
        legendEl.innerHTML = '';
        datasets.forEach(ds => {
            const item = document.createElement('span');
            item.className = 'legend-item';
            const swatch = document.createElement('span');
            swatch.className = 'legend-swatch';
            swatch.style.backgroundColor = ds.borderColor;
            const label = document.createElement('span');
            label.textContent = ds.label;
            item.appendChild(swatch);
            item.appendChild(label);
            legendEl.appendChild(item);
        });

        const dataPointCount = labels.length;
        const minPixelsPerPoint = 50;
        const calculatedWidth = Math.max(scrollEl.clientWidth, dataPointCount * minPixelsPerPoint);
        const calculatedHeight = scrollEl.clientHeight || 250;

        canvas.width = calculatedWidth;
        canvas.height = calculatedHeight;

        charts[elementId] = new Chart(canvas, {
            type: 'line',
            data: { labels, datasets },
            options: {
                responsive: false,
                maintainAspectRatio: false,
                plugins: {
                    title: { display: false },
                    legend: { display: false },
                    tooltip: {
                        backgroundColor: '#000',
                        titleColor: '#fff',
                        bodyColor: '#fff',
                        borderColor: getChartColor(0),
                        borderWidth: 1
                    }
                },
                scales: {
                    x: {
                        grid: { color: '#333', display: true },
                        ticks: { color: '#fff', font: { family: 'sans-serif', size: 11, weight: 'bold' }, maxTicksLimit: 8 }
                    },
                    y: {
                        grid: { color: '#333', display: true },
                        ticks: { color: '#fff', font: { family: 'sans-serif', size: 11, weight: 'bold' } }
                    }
                }
            }
        });

        if (!container.querySelector('.chart-hint')) {
            const hint = document.createElement('div');
            hint.className = 'chart-hint';
            hint.textContent = 'Scroll left/right to view more data';
            container.appendChild(hint);
        }
    }

    function getChartColor(index) {
        const root = document.documentElement;
        const r = getComputedStyle(root).getPropertyValue('--accent-r').trim();
        const g = getComputedStyle(root).getPropertyValue('--accent-g').trim();
        const b = getComputedStyle(root).getPropertyValue('--accent-b').trim();
        const accentColor = "rgb(" + r + "," + g + "," + b + ")";
        const colors = [accentColor, '#ff9800', '#2196f3', '#f44336', '#9c27b0', '#00bcd4'];
        return colors[index % colors.length];
    }

    async function updateStats() {
        const sessionSelect = document.getElementById("session");
        const session = sessionSelect?.options[sessionSelect.selectedIndex]?.text || 'Current';
        const params = session === 'Current' ? '' : '?session=' + encodeURIComponent(session);

        const summary = await fetchData('/plugins/session-stats/summary' + params);
        if (summary.networks !== undefined) {
            document.getElementById('stat_networks').textContent = summary.networks;
            document.getElementById('stat_handshakes').textContent = summary.handshakes;
            document.getElementById('stat_deauths').textContent = summary.deauths;
            document.getElementById('stat_duration').textContent = summary.duration;
            document.getElementById('stat_temp').textContent = summary.temp || '0°C';
            document.getElementById('stat_mem').textContent = summary.mem || '0%';
            document.getElementById('stat_cpu').textContent = summary.cpu || '0%';
        }

        const chartConfigs = [
            { endpoint: 'networks', id: 'chart_networks', title: 'Networks Captured' },
            { endpoint: 'handshakes', id: 'chart_handshakes', title: 'Handshakes Captured' },
            { endpoint: 'deauths', id: 'chart_deauths', title: 'Deauthentications Sent' },
            { endpoint: 'temp', id: 'chart_temp', title: 'Temperature (°C)' },
            { endpoint: 'mem', id: 'chart_mem', title: 'Memory Usage (%)' },
            { endpoint: 'cpu', id: 'chart_cpu', title: 'CPU Load (%)' }
        ];

        for (const config of chartConfigs) {
            const data = await fetchData('/plugins/session-stats/' + config.endpoint + params);
            createChart(config.id, config.title, data);
        }
    }

    async function loadSessionFiles() {
        const data = await fetchData('/plugins/session-stats/sessions');
        const select = document.getElementById("session");
        data.files?.forEach(file => {
            const option = document.createElement("option");
            option.text = file;
            select.appendChild(option);
        });
        select.addEventListener('change', updateStats);
    }

    document.addEventListener('DOMContentLoaded', () => {
        loadSessionFiles();
        updateStats();
        setInterval(updateStats, 30000);
    });
</script>
</body>
</html>
`
