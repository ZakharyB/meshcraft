# Paper plugin (Java integration artifact)
The only Java in the project. It writes a health heartbeat JSON
(players online, version) the Go agent can read, nothing more.
Build with JDK 21 + Maven: `mvn package`. Not built by the Go CI job;
a separate optional CI job builds it when a JDK is available.
