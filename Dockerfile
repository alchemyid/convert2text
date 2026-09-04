FROM python:3.12-slim

WORKDIR /app

# Install system dependencies if required
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install Python requirements
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application files
COPY app/ app/
COPY static/ static/
COPY .env.example .env

# Expose port
EXPOSE 8080

ENV PORT=8080 \
    PYTHONUNBUFFERED=1

CMD ["python", "-m", "app.main", "--serve", "--port", "8080"]
