import http from 'k6/http';
import { check, fail } from 'k6';

export const BASE_URL = (__ENV.BASE_URL || 'http://localhost:8080').replace(/\/$/, '');
export const LOAD_EMAIL = __ENV.LOAD_EMAIL || '';
export const LOAD_PIN = __ENV.LOAD_PIN || '';
export const LOAD_API_KEY = __ENV.LOAD_API_KEY || '';
export const VUS = Number.parseInt(__ENV.VUS || '10', 10);
export const DURATION = __ENV.DURATION || '30s';

export const summaryTrendStats = ['avg', 'med', 'p(95)', 'p(99)', 'min', 'max'];

let token = '';

export function obtenerToken() {
  if (token) {
    return token;
  }
  if (!LOAD_EMAIL || !LOAD_PIN) {
    fail('Defina LOAD_EMAIL y LOAD_PIN antes de ejecutar el escenario.');
  }

  const respuesta = http.post(
    `${BASE_URL}/api/auth/login`,
    JSON.stringify({ correo: LOAD_EMAIL, pin: LOAD_PIN }),
    {
      headers: { 'Content-Type': 'application/json' },
      tags: { endpoint: 'login' },
      timeout: '60s',
    },
  );
  let cuerpo = {};
  try {
    cuerpo = respuesta.json();
  } catch (_) {
    // El check siguiente deja visible una respuesta no JSON sin romper k6.
  }
  const valido = check(respuesta, {
    'login devuelve 200': (r) => r.status === 200,
    'login devuelve token': () => typeof cuerpo.token === 'string' && cuerpo.token.length > 0,
  });
  if (!valido) {
    fail(`No fue posible iniciar sesión: HTTP ${respuesta.status}`);
  }
  token = cuerpo.token;
  return token;
}

export function parametrosAutenticados(endpoint) {
  return {
    headers: { Authorization: `Bearer ${obtenerToken()}` },
    tags: { endpoint },
    timeout: '60s',
  };
}

export function esJSONExitoso(respuesta) {
  if (respuesta.status !== 200) {
    return false;
  }
  try {
    return respuesta.json('ok') === true;
  } catch (_) {
    return false;
  }
}
