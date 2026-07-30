FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY sync.py auth.py inspect_api.py schema_validate.py ./
COPY schemas/ ./schemas/

CMD ["python", "sync.py"]
