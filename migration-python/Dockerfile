FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

ENTRYPOINT ["python", "migrate_sheets_to_postgres.py"]
CMD ["--exceltec", "/app/data/BD_Cotizador_Exceltec.xlsx", "--parametros", "/app/data/BD_Cotizador_Parametros.xlsx"]
