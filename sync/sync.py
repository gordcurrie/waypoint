import logging
import time

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
)
log = logging.getLogger(__name__)

if __name__ == "__main__":
    log.info("Garmin sync sidecar — not yet implemented")
    while True:
        time.sleep(3600)
