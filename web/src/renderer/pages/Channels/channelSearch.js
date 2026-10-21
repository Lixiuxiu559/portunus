// Local directory filtering only; credentials are never part of the search index.
export function filterChannels(channels, search) {
  const terms = search.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  if (!terms.length) return channels;
  return channels.filter((channel) => {
    const text = [channel.name, channel.base_url, channel.provider_id, channel.type]
      .filter(Boolean).join(' ').toLocaleLowerCase();
    return terms.every((term) => text.includes(term));
  });
}
