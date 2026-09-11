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
    'http_req_duration{endpoint:dashboard}': ['p(95)<1500'],
    'http_req_failed{endpoint:dashboard}': ['rate<0.01'],
  },
};

export default function () {
  const respuesta = http.get(
    `${BASE_URL}/api/dashboard`,
    parametrosAutenticados('dashboard'),
  );
  check(respuesta, {
    'dashboard responde correctamente': esJSONExitoso,
  });
  sleep(1);
}
