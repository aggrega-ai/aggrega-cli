// The researcher's graph. The importer never reads this file: the import proves
// it by importing the application with this file made unreadable.
import { END, MessagesAnnotation, START, StateGraph } from "@langchain/langgraph";

export const graph = new StateGraph(MessagesAnnotation)
  .addNode("research", async () => ({ messages: [] }))
  .addEdge(START, "research")
  .addEdge("research", END)
  .compile();
