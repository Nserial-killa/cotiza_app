import http from 'k6/http';
import { check, fail, sleep } from 'k6';
import exec from 'k6/execution';

import {
  BASE_URL,
  DURATION,
  LOAD_API_KEY,
  VUS,
  summaryTrendStats,
} from './lib/config.js';

export const options = {
  vus: VUS,
  duration: DURATION,
  summaryTrendStats,
  thresholds: {
    checks: ['rate>0.99'],
    'http_req_duration{endpoint:solicitud_externa}': ['p(95)<500'],
    'http_req_failed{endpoint:solicitud_externa}': ['rate<0.01'],
  },
};

export default function () {
  if (!LOAD_API_KEY) {
    fail('Defina LOAD_API_KEY antes de ejecutar el escenario.');
  }
  const idEvento = `${exec.vu.idInTest}-${exec.scenario.iterationInTest}-${Date.now()}`;
  const respuesta = http.post(
    `${BASE_URL}/api/externo/solicitudes`,
    JSON.stringify({
      cliente_nombre: `Cliente ráfaga ${idEvento}`,
      contacto_nombre: 'Contacto de carga',
      contacto_correo: 'carga@example.test',
      calculadora_id: 'LOAD-CALC-01',
      descripcion: 'Solicitud generada por la auditoría de performance.',
    }),
    {
      headers: {
        'Content-Type': 'application/json',
        'X-Api-Key': LOAD_API_KEY,
        'Idempotency-Key': `load:k6:${idEvento}`,
      },
      tags: { endpoint: 'solicitud_externa' },
      timeout: '60s',
    },
  );
  check(respuesta, {
    'solicitud externa devuelve 201': (r) => r.status === 201,
    'solicitud externa devuelve id': (r) => {
      try {
        return typeof r.json('solicitud_id') === 'string';
      } catch (_) {
        return false;
      }
    },
  });
  sleep(0.2);
}
