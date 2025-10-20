FROM n8nio/n8n:latest

# Expose the default n8n port
EXPOSE 5678

# Set default environment variables
ENV N8N_PORT=5678
ENV N8N_PROTOCOL=https
ENV WEBHOOK_URL=https://your-n8n-url.railway.app/

# Start n8n
CMD ["n8n"]
