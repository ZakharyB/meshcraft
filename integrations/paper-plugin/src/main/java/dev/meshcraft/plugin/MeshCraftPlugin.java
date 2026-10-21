package dev.meshcraft.plugin;

import org.bukkit.Bukkit;
import org.bukkit.plugin.java.JavaPlugin;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;

/**
 * MeshCraft Paper integration plugin.
 *
 * Deliberately tiny: the Go agent owns lifecycle, networking and
 * security. This plugin only exposes in-server facts the agent cannot
 * see from outside — player count and a health heartbeat file —
 * and a /meshcraft status command for operators.
 *
 * Build: mvn package (requires JDK 21 and network access to the
 * PaperMC Maven repository). Output: target/meshcraft-plugin.jar,
 * dropped into the world dir's plugins/ folder.
 */
public final class MeshCraftPlugin extends JavaPlugin {

    private Path statusFile;

    @Override
    public void onEnable() {
        statusFile = getDataFolder().toPath().resolve("meshcraft-status.json");
        try {
            Files.createDirectories(getDataFolder().toPath());
        } catch (IOException e) {
            getLogger().warning("MeshCraft: cannot create data folder: " + e.getMessage());
        }
        // Heartbeat every 5 seconds (100 ticks).
        Bukkit.getScheduler().runTaskTimer(this, this::writeStatus, 20L, 100L);
        getLogger().info("MeshCraft integration plugin enabled");
    }

    private void writeStatus() {
        String json = "{"
            + "\"online\":true,"
            + "\"players_online\":" + Bukkit.getOnlinePlayers().size() + ","
            + "\"max_players\":" + Bukkit.getMaxPlayers() + ","
            + "\"version\":\"" + Bukkit.getVersion() + "\","
            + "\"epoch_ms\":" + System.currentTimeMillis()
            + "}";
        try {
            Files.write(statusFile, json.getBytes(StandardCharsets.UTF_8));
        } catch (IOException e) {
            getLogger().warning("MeshCraft: cannot write status file: " + e.getMessage());
        }
    }
}
