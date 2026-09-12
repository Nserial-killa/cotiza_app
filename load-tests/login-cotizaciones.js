import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';

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
    'http_req_duration{endpoint:cotizaciones}': ['p(95)<500'],
    'http_req_failed{endpoint:cotizaciones}': ['rate<0.01'],
  },
};

const estados = ['Borrador', 'Revisión Comercial', 'Enviada al Cliente', 'Ganada', 'Perdida'];

export default function () {
  const estado = estados[exec.scenario.iterationInTest % estados.length];
  const consulta = `calculadora_id=LOAD-CALC-01&filtro_usuario_id=load-user&fecha_desde=2025-01-01&estado=${encodeURIComponent(estado)}`;
  const respuesta = http.get(
    `${BASE_URL}/api/cotizaciones?${consulta}`,
    parametrosAutenticados('cotizaciones'),
  );
  check(respuesta, {
    'listado de cotizaciones responde correctamente': esJSONExitoso,
  });
  sleep(1);
}
