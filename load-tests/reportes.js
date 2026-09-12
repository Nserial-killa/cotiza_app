import http from 'k6/http';
import { check, sleep } from 'k6';

import {
  BASE_URL,
  DURATION,
  VUS,
  esJSONExitoso,
  parametrosAutenticados,
  summaryTrendStats,
} from './lib/config.js';

export const options = {
  vus: VUS,
  duration: DURATION,
  summaryTrendStats,
  thresholds: {
    checks: ['rate>0.99'],
    'http_req_duration{endpoint:login}': ['p(95)<1500'],
    'http_req_failed{endpoint:login}': ['rate<0.01'],
    'http_req_duration{endpoint:reporte_json}': ['p(95)<1500'],
    'http_req_failed{endpoint:reporte_json}': ['rate<0.01'],
    'http_req_duration{endpoint:reporte_csv}': ['p(95)<1500'],
    'http_req_failed{endpoint:reporte_csv}': ['rate<0.01'],
  },
};

const filtros = 'fecha_desde=2025-01-01&fecha_hasta=2026-12-31&calculadora_id=LOAD-CALC-01';

export default function () {
  const reporte = http.get(
    `${BASE_URL}/api/reportes/cotizaciones?${filtros}`,
    parametrosAutenticados('reporte_json'),
  );
  check(reporte, {
    'reporte JSON responde correctamente': esJSONExitoso,
  });

  const csv = http.get(
    `${BASE_URL}/api/reportes/cotizaciones/exportar?${filtros}`,
    parametrosAutenticados('reporte_csv'),
  );
  check(csv, {
    'exportación CSV devuelve 200': (r) => r.status === 200,
    'exportación CSV usa text/csv': (r) => String(r.headers['Content-Type'] || '').includes('text/csv'),
  });
  sleep(1);
}
