# Default worker image: Alpine + the Anthropic `ant` CLI.
#
# Operational requirement (SPEC.md §12): self-hosted sandbox workers require
# /bin/bash at exactly that path. Alpine satisfies this once `bash` is added;
# distroless will NOT work for worker pods.
FROM alpine:3.20

# Pin the ant CLI version so worker behavior is reproducible. Bump deliberately.
ARG ANT_VERSION=1.9.1

RUN apk add --no-cache bash ca-certificates libstdc++ libgcc

# The `ant` CLI is distributed by Anthropic. Supply the install step for your
# environment here (e.g. download the pinned release for your architecture into
# /usr/local/bin/ant and chmod +x). This is intentionally left as a placeholder
# rather than hard-coding an unverified download URL — see SPEC.md §15.
#
# Example shape (adapt the source to Anthropic's published distribution):
#   ADD https://<anthropic-ant-release-url>/ant-${ANT_VERSION}-linux-amd64 /usr/local/bin/ant
#   RUN chmod +x /usr/local/bin/ant
#
# Until that is wired up, builds of this image will not contain `ant`.

# Default working directory; skills download to /workspace/skills/<name>/.
WORKDIR /workspace

# Workers authenticate with ANTHROPIC_ENVIRONMENT_KEY (injected by CSC).
ENTRYPOINT ["ant"]
CMD ["beta:worker", "poll", "--workdir", "/workspace"]
